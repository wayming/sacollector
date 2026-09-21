package tradingeconomics

import "sort"

// Instrument identifies one series on the Trading Economics charts API.
type Instrument struct {
	Symbol string // charts symbol, e.g. "gacgb10"
	Name   string // human-readable name for display
}

// Bond10YInstruments maps ISO 3166-1 alpha-2 country codes to the charts
// instrument carrying that country's 10-year generic government bond yield.
// Only symbols that have been verified against the live API are listed.
var Bond10YInstruments = map[string]Instrument{
	"AU": {Symbol: "gacgb10", Name: "Australia 10Y Government Bond Yield"},
}

// SupportedBond10YCountries returns the sorted country codes that
// FetchBond10Y accepts.
func SupportedBond10YCountries() []string {
	codes := make([]string, 0, len(Bond10YInstruments))
	for code := range Bond10YInstruments {
		codes = append(codes, code)
	}
	sort.Strings(codes)
	return codes
}

// Observation is a single point of a yield series.
//
// ChangePercent and Change are nil for the first point of a series, which has
// no prior value to compare against.
type Observation struct {
	Time          string   `json:"time"` // RFC 3339, rendered in the series timezone
	Value         float64  `json:"value"`
	ChangePercent *float64 `json:"change_pct"`
	Change        *float64 `json:"change"`
}

// BondYieldSeries is the result of a 10-year bond yield query, ready to be
// serialised for an MCP tool result or an HTTP response.
type BondYieldSeries struct {
	CountryCode string        `json:"country_code"`
	Symbol      string        `json:"symbol"`
	Name        string        `json:"name"`
	Timezone    string        `json:"timezone"`
	Count       int           `json:"count"`
	FirstDate   string        `json:"first_date"`
	LastDate    string        `json:"last_date"`
	LatestValue float64       `json:"latest_value"`
	Data        []Observation `json:"data"`
}
