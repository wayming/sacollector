package parser

import (
	"encoding/json"
	"fmt"
	"strings"
)

// ScreenerResponse represents the top-level JSON from the screener API.
type ScreenerResponse struct {
	Status int         `json:"status"`
	Data   ScreenerData `json:"data"`
}

// ScreenerData holds the inner data.
type ScreenerData struct {
	Data         []StockItem `json:"data"`
	ResultsCount int         `json:"resultsCount"`
}

// StockItem represents a single stock from the screener API.
type StockItem struct {
	No        int     `json:"no"`
	S         string  `json:"s"`        // e.g., "hkg/0700"
	N         string  `json:"n"`        // e.g., "Tencent Holdings Limited"
	MarketCap float64 `json:"marketCap"`
	Price     float64 `json:"price"`
	Change    float64 `json:"change"`
	Revenue   float64 `json:"revenue"`
}

// StockInfo is the parsed and cleaned stock info.
type StockInfo struct {
	Code      string  `json:"code"`
	Name      string  `json:"name"`
	MarketCap float64 `json:"market_cap"`
	Price     float64 `json:"price"`
	Change    float64 `json:"change"`
	Revenue   float64 `json:"revenue"`
}

// ExtractCode extracts the numeric stock code from the "s" field (e.g., "hkg/0700" → "0700").
func ExtractCode(s string) string {
	parts := strings.Split(s, "/")
	if len(parts) >= 2 {
		return parts[1]
	}
	return s
}

// ParseScreener parses the raw screener API JSON response.
// Returns a slice of StockInfo and the total results count.
func ParseScreener(raw []byte) ([]StockInfo, int, error) {
	var resp ScreenerResponse
	if err := json.Unmarshal(raw, &resp); err != nil {
		return nil, 0, fmt.Errorf("unmarshaling screener response: %w", err)
	}

	if resp.Status != 200 {
		return nil, 0, fmt.Errorf("API returned status %d", resp.Status)
	}

	stocks := make([]StockInfo, 0, len(resp.Data.Data))
	for _, item := range resp.Data.Data {
		code := ExtractCode(item.S)
		if code == "" {
			continue
		}
		stocks = append(stocks, StockInfo{
			Code:      code,
			Name:      item.N,
			MarketCap: item.MarketCap,
			Price:     item.Price,
			Change:    item.Change,
			Revenue:   item.Revenue,
		})
	}

	return stocks, resp.Data.ResultsCount, nil
}

// BuildStockMap creates a map keyed by stock code from a slice of StockInfo.
func BuildStockMap(stocks []StockInfo) map[string]StockInfo {
	result := make(map[string]StockInfo, len(stocks))
	for _, s := range stocks {
		result[s.Code] = s
	}
	return result
}
