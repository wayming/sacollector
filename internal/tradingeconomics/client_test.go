package tradingeconomics

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/base64"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// encodePayload builds a charts response the way the endpoint serves it:
// gzip -> repeating-key XOR -> base64 -> JSON string. The JSON string wrapper
// is what the real endpoint returns, so it has to be mirrored here.
func encodePayload(t *testing.T, plain string) []byte {
	t.Helper()

	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	if _, err := zw.Write([]byte(plain)); err != nil {
		t.Fatalf("gzip write: %v", err)
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("gzip close: %v", err)
	}

	key := []byte(ObfuscationKey)
	masked := make([]byte, buf.Len())
	for i, b := range buf.Bytes() {
		masked[i] = b ^ key[i%len(key)]
	}

	return []byte(`"` + base64.StdEncoding.EncodeToString(masked) + `"`)
}

func brisbane(t *testing.T) *time.Location {
	t.Helper()

	loc, err := time.LoadLocation(DefaultTimezone)
	if err != nil {
		t.Fatalf("LoadLocation(%q): %v", DefaultTimezone, err)
	}
	return loc
}

func TestDataMagicDecodesSeries(t *testing.T) {
	// [timestamp, value, changePct, change]; the first row has no prior point
	// to compare against, so its change columns are null.
	plain := `{"series":[{"data":[
		[1474848000, 1.961, null, null],
		[1475452800, 2.183, 11.321, 0.222]
	]}]}`

	points, err := dataMagic(encodePayload(t, plain), []byte(ObfuscationKey), brisbane(t))
	if err != nil {
		t.Fatalf("dataMagic: %v", err)
	}
	if len(points) != 2 {
		t.Fatalf("got %d points, want 2", len(points))
	}

	first := points[0]
	if got, want := first.Time.Format(time.RFC3339), "2016-09-26T10:00:00+10:00"; got != want {
		t.Errorf("first timestamp = %q, want %q", got, want)
	}
	if first.Value != 1.961 {
		t.Errorf("first value = %v, want 1.961", first.Value)
	}
	if first.ChangePercent != nil || first.Change != nil {
		t.Errorf("first changes = (%v, %v), want both nil", first.ChangePercent, first.Change)
	}

	second := points[1]
	if second.ChangePercent == nil || *second.ChangePercent != 11.321 {
		t.Errorf("second change pct = %v, want 11.321", second.ChangePercent)
	}
	if second.Change == nil || *second.Change != 0.222 {
		t.Errorf("second change = %v, want 0.222", second.Change)
	}
}

func TestDataMagicErrors(t *testing.T) {
	cases := []struct {
		name    string
		payload []byte
		key     []byte
	}{
		{name: "invalid base64", payload: []byte("!!! not base64 !!!"), key: []byte(ObfuscationKey)},
		{name: "empty key", payload: encodePayload(t, `{"series":[]}`), key: nil},
		{name: "no series", payload: encodePayload(t, `{"series":[]}`), key: []byte(ObfuscationKey)},
		{name: "not JSON", payload: encodePayload(t, `not json at all`), key: []byte(ObfuscationKey)},
		{name: "non-timestamp first field", payload: encodePayload(t, `{"series":[{"data":[["2016-09-26",1.961,null,null]]}]}`), key: []byte(ObfuscationKey)},
		{name: "null value", payload: encodePayload(t, `{"series":[{"data":[[1474848000,null,null,null]]}]}`), key: []byte(ObfuscationKey)},
		{name: "short row", payload: encodePayload(t, `{"series":[{"data":[[1474848000]]}]}`), key: []byte(ObfuscationKey)},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := dataMagic(tc.payload, tc.key, time.UTC); err == nil {
				t.Fatal("dataMagic returned nil error, want an error")
			}
		})
	}
}

// A series with no rows decodes to zero points without error; the caller
// decides whether that is acceptable.
func TestDataMagicEmptyDataIsNotAnError(t *testing.T) {
	points, err := dataMagic(encodePayload(t, `{"series":[{"data":[]}]}`), []byte(ObfuscationKey), time.UTC)
	if err != nil {
		t.Fatalf("dataMagic: %v", err)
	}
	if len(points) != 0 {
		t.Fatalf("got %d points, want 0", len(points))
	}
}

// A payload saved without its JSON string wrapper must still decode.
func TestDataMagicAcceptsUnquotedPayload(t *testing.T) {
	quoted := encodePayload(t, `{"series":[{"data":[[1474848000, 1.961, null, null]]}]}`)
	bare := bytes.Trim(quoted, `"`)

	points, err := dataMagic(bare, []byte(ObfuscationKey), time.UTC)
	if err != nil {
		t.Fatalf("dataMagic on bare payload: %v", err)
	}
	if len(points) != 1 {
		t.Fatalf("got %d points, want 1", len(points))
	}
	if points[0].Value != 1.961 {
		t.Errorf("value = %v, want 1.961", points[0].Value)
	}
}

func TestSupportedBond10YCountries(t *testing.T) {
	codes := SupportedBond10YCountries()
	if len(codes) != 1 || codes[0] != "AU" {
		t.Fatalf("SupportedBond10YCountries() = %v, want [AU]", codes)
	}
}

// Unsupported or blank country codes must fail without touching the network.
func TestFetchBond10YRejectsUnsupportedCountry(t *testing.T) {
	client := NewClient()

	for _, code := range []string{"", "   ", "US", "zz"} {
		t.Run(fmt.Sprintf("%q", code), func(t *testing.T) {
			_, err := client.FetchBond10Y(context.Background(), code)
			if err == nil {
				t.Fatalf("FetchBond10Y(%q) = nil error, want an error", code)
			}
			if !strings.Contains(err.Error(), "AU") {
				t.Errorf("error %q should list the supported codes", err)
			}
		})
	}
}

func TestFetchBond10YBuildsRequestAndParses(t *testing.T) {
	var gotPath, gotAPIKey string
	payload := encodePayload(t, `{"series":[{"data":[[1474848000, 1.961, null, null]]}]}`)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.RequestURI()
		gotAPIKey = r.Header.Get("x-api-key")
		w.Write(payload)
	}))
	defer srv.Close()

	client := NewClient()
	client.baseURL = srv.URL

	// Lower-case input must resolve to the same instrument.
	series, err := client.FetchBond10Y(context.Background(), "au")
	if err != nil {
		t.Fatalf("FetchBond10Y: %v", err)
	}

	if want := "/markets/gacgb10:ind?span=" + ChartSpan + "&ohlc=0"; gotPath != want {
		t.Errorf("request path = %q, want %q", gotPath, want)
	}
	if gotAPIKey != DefaultAPIKey {
		t.Errorf("x-api-key = %q, want %q", gotAPIKey, DefaultAPIKey)
	}

	instr := Bond10YInstruments["AU"]
	if series.CountryCode != "AU" {
		t.Errorf("country code = %q, want AU", series.CountryCode)
	}
	if series.Symbol != instr.Symbol {
		t.Errorf("symbol = %q, want %q", series.Symbol, instr.Symbol)
	}
	if series.Name != instr.Name {
		t.Errorf("name = %q, want %q", series.Name, instr.Name)
	}
	if series.Timezone != DefaultTimezone {
		t.Errorf("timezone = %q, want %q", series.Timezone, DefaultTimezone)
	}
	if series.Count != 1 || len(series.Data) != 1 {
		t.Fatalf("count = %d and %d data rows, want 1 and 1", series.Count, len(series.Data))
	}
	if series.FirstDate != "2016-09-26T10:00:00+10:00" || series.LastDate != series.FirstDate {
		t.Errorf("dates = (%q, %q), want both 2016-09-26T10:00:00+10:00", series.FirstDate, series.LastDate)
	}
	if series.LatestValue != 1.961 {
		t.Errorf("latest value = %v, want 1.961", series.LatestValue)
	}
}

// An unknown symbol still answers HTTP 200, just with no series.
func TestFetchBond10YNoSeries(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write(encodePayload(t, `{"series":[]}`))
	}))
	defer srv.Close()

	client := NewClient()
	client.baseURL = srv.URL

	if _, err := client.FetchBond10Y(context.Background(), "AU"); err == nil {
		t.Fatal("want an error when the payload carries no series")
	}
}
