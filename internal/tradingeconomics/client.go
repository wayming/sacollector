// Package tradingeconomics fetches and decrypts series from the Trading
// Economics charts API.
//
// The public charts endpoint returns an obfuscated payload rather than plain
// JSON: the body is base64-encoded, XOR-encrypted against a fixed front-end
// key, and gzip-compressed. Decoding is therefore
// base64 -> repeating-key XOR -> gzip -> JSON. The scheme is a port of
// sandbox/trading_economics.py.
package tradingeconomics

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

const (
	// ObfuscationKey is the fixed XOR key used by the Trading Economics
	// front-end "dataMagic" routine.
	ObfuscationKey = "tradingeconomics-charts-core-api-key"

	// DefaultAPIKey is sent as the x-api-key header.
	DefaultAPIKey = "20260324:loboantunes"

	// BaseURL is the Trading Economics charts CDN.
	BaseURL = "https://d3ii0wo49og5mi.cloudfront.net"

	// DefaultTimezone is the zone data-point timestamps are rendered in.
	DefaultTimezone = "Australia/Brisbane"

	// DefaultUserAgent mimics the browser client the endpoint expects.
	DefaultUserAgent = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) " +
		"AppleWebKit/537.36 (KHTML, like Gecko) Chrome/124.0.0.0 Safari/537.36"

	// ChartSpan is the lookback window requested from the charts API. The
	// endpoint samples it weekly, yielding ~522 points over 10 years.
	ChartSpan = "10y"

	// requestTimeout bounds a single charts request.
	requestTimeout = 30 * time.Second

	// Timestamps are Unix seconds. Values outside this range are treated as
	// ordinary numbers rather than dates, mirroring the reference script's
	// is_timestamp guard so a value like 1.961 is never read as a date.
	minUnixSeconds = 1_000_000_000
	maxUnixSeconds = 2_000_000_000
)

// Point is one decrypted data point. ChangePercent and Change are nil when the
// series has no prior point to compare against.
type Point struct {
	Time          time.Time
	Value         float64
	ChangePercent *float64
	Change        *float64
}

// Client fetches series from the Trading Economics charts API.
type Client struct {
	http           *http.Client
	apiKey         string
	obfuscationKey []byte
	timezone       string
	baseURL        string
}

// NewClient returns a Client configured with the default credentials, CDN and
// timezone.
func NewClient() *Client {
	return &Client{
		http: &http.Client{
			Timeout: requestTimeout,
			Transport: &http.Transport{
				MaxIdleConns:        10,
				MaxIdleConnsPerHost: 4,
				IdleConnTimeout:     90 * time.Second,
			},
		},
		apiKey:         DefaultAPIKey,
		obfuscationKey: []byte(ObfuscationKey),
		timezone:       DefaultTimezone,
		baseURL:        BaseURL,
	}
}

// Timezone returns the zone this client renders timestamps in.
func (c *Client) Timezone() string { return c.timezone }

// FetchBond10Y fetches the 10-year government bond yield series for a country
// from the last 10 years.
//
// countryCode is an ISO 3166-1 alpha-2 code and is case-insensitive. An error
// listing the supported codes is returned for any country without a verified
// instrument.
func (c *Client) FetchBond10Y(ctx context.Context, countryCode string) (*BondYieldSeries, error) {
	supported := strings.Join(SupportedBond10YCountries(), ", ")

	code := strings.ToUpper(strings.TrimSpace(countryCode))
	if code == "" {
		return nil, fmt.Errorf("country code is required (supported: %s)", supported)
	}

	instr, ok := Bond10YInstruments[code]
	if !ok {
		return nil, fmt.Errorf("unsupported country code %q (supported: %s)", countryCode, supported)
	}

	loc, err := time.LoadLocation(c.timezone)
	if err != nil {
		return nil, fmt.Errorf("loading timezone %q: %w", c.timezone, err)
	}

	url := fmt.Sprintf("%s/markets/%s:ind?span=%s&ohlc=0", c.baseURL, instr.Symbol, ChartSpan)
	body, err := c.get(ctx, url)
	if err != nil {
		return nil, err
	}

	points, err := dataMagic(body, c.obfuscationKey, loc)
	if err != nil {
		return nil, fmt.Errorf("decoding series for %s: %w", instr.Symbol, err)
	}
	if len(points) == 0 {
		return nil, fmt.Errorf("no data points returned for %s", instr.Symbol)
	}

	return buildSeries(code, instr, c.timezone, points), nil
}

// buildSeries flattens decoded points into the JSON-ready result shape.
func buildSeries(code string, instr Instrument, timezone string, points []Point) *BondYieldSeries {
	data := make([]Observation, 0, len(points))
	for _, p := range points {
		data = append(data, Observation{
			Time:          p.Time.Format(time.RFC3339),
			Value:         p.Value,
			ChangePercent: p.ChangePercent,
			Change:        p.Change,
		})
	}

	first, last := data[0], data[len(data)-1]

	return &BondYieldSeries{
		CountryCode: code,
		Symbol:      instr.Symbol,
		Name:        instr.Name,
		Timezone:    timezone,
		Count:       len(data),
		FirstDate:   first.Time,
		LastDate:    last.Time,
		LatestValue: last.Value,
		Data:        data,
	}
}

// get performs the charts request and returns the still-obfuscated body.
func (c *Client) get(ctx context.Context, url string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("building request for %s: %w", url, err)
	}
	req.Header.Set("x-api-key", c.apiKey)
	req.Header.Set("User-Agent", DefaultUserAgent)
	req.Header.Set("Accept", "*/*")

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("requesting %s: %w", url, err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("reading response from %s: %w", url, err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d from %s: %s", resp.StatusCode, url, truncate(body, 300))
	}

	return body, nil
}

// dataMagic reverses the front-end obfuscation and returns the decoded points
// in loc.
func dataMagic(payload []byte, key []byte, loc *time.Location) ([]Point, error) {
	if len(key) == 0 {
		return nil, fmt.Errorf("obfuscation key must not be empty")
	}

	payload = bytes.TrimSpace(payload)

	// The endpoint serves the payload as a JSON string, so the body arrives
	// wrapped in double quotes. Unwrap it before decoding; a bare payload
	// (e.g. one saved from a previous response) is accepted as-is.
	//
	// The reference script could skip this step because Python's
	// base64.b64decode silently discards characters outside the alphabet.
	if len(payload) > 0 && payload[0] == '"' {
		var encoded string
		if err := json.Unmarshal(payload, &encoded); err != nil {
			return nil, fmt.Errorf("unquoting payload: %w", err)
		}
		payload = []byte(encoded)
	}

	decoded, err := base64.StdEncoding.DecodeString(string(payload))
	if err != nil {
		return nil, fmt.Errorf("base64 decode: %w", err)
	}

	unmasked := make([]byte, len(decoded))
	for i, b := range decoded {
		unmasked[i] = b ^ key[i%len(key)]
	}

	zr, err := gzip.NewReader(bytes.NewReader(unmasked))
	if err != nil {
		return nil, fmt.Errorf("opening gzip stream: %w", err)
	}
	defer zr.Close()

	raw, err := io.ReadAll(zr)
	if err != nil {
		return nil, fmt.Errorf("gunzip: %w", err)
	}

	// The chart data is wrapped in an envelope:
	//   {"series":[{"data":[[timestamp, value, changePct, change], ...]}]}
	var envelope struct {
		Series []struct {
			Data [][]json.RawMessage `json:"data"`
		} `json:"series"`
	}
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return nil, fmt.Errorf("parsing payload JSON: %w", err)
	}
	// An unknown symbol still returns HTTP 200, just with no series. The
	// reference script raised IndexError here.
	if len(envelope.Series) == 0 {
		return nil, fmt.Errorf("payload contains no series (unknown symbol or empty chart)")
	}

	return parsePoints(envelope.Series[0].Data, loc)
}

// parsePoints converts raw data rows into Points, rendering each timestamp in
// loc. Rows are [timestamp, value, changePct, change], where the two change
// columns may be null or absent.
func parsePoints(rows [][]json.RawMessage, loc *time.Location) ([]Point, error) {
	points := make([]Point, 0, len(rows))

	for i, row := range rows {
		if len(row) < 2 {
			return nil, fmt.Errorf("row %d: expected at least 2 fields, got %d", i, len(row))
		}

		secs, ok := asUnixSeconds(row[0])
		if !ok {
			return nil, fmt.Errorf("row %d: first field is not a Unix timestamp: %s", i, row[0])
		}

		value, err := asFloat(row[1])
		if err != nil {
			return nil, fmt.Errorf("row %d: value: %w", i, err)
		}

		point := Point{
			Time:  time.Unix(secs, 0).In(loc),
			Value: value,
		}
		if len(row) > 2 {
			if point.ChangePercent, err = asOptionalFloat(row[2]); err != nil {
				return nil, fmt.Errorf("row %d: change percent: %w", i, err)
			}
		}
		if len(row) > 3 {
			if point.Change, err = asOptionalFloat(row[3]); err != nil {
				return nil, fmt.Errorf("row %d: change: %w", i, err)
			}
		}

		points = append(points, point)
	}

	return points, nil
}

// asUnixSeconds reports whether raw holds a JSON number that looks like a Unix
// timestamp in seconds.
func asUnixSeconds(raw json.RawMessage) (int64, bool) {
	var f float64
	if err := json.Unmarshal(raw, &f); err != nil {
		return 0, false
	}
	if f < minUnixSeconds || f > maxUnixSeconds {
		return 0, false
	}
	return int64(f), true
}

// asFloat decodes a non-null JSON number.
func asFloat(raw json.RawMessage) (float64, error) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return 0, fmt.Errorf("expected a number, got %s", trimmed)
	}

	var f float64
	if err := json.Unmarshal(trimmed, &f); err != nil {
		return 0, fmt.Errorf("expected a number, got %s", trimmed)
	}
	return f, nil
}

// asOptionalFloat decodes a nullable JSON number, returning nil for null.
func asOptionalFloat(raw json.RawMessage) (*float64, error) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return nil, nil
	}
	f, err := asFloat(trimmed)
	if err != nil {
		return nil, err
	}
	return &f, nil
}

// truncate shortens a response body for inclusion in an error message.
func truncate(b []byte, n int) string {
	if len(b) <= n {
		return string(b)
	}
	return string(b[:n]) + "..."
}
