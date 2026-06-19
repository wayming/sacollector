package screener

import (
	"fmt"
	"log"
	"strings"

	"sacollector/internal/client"
	"sacollector/internal/parser"
)

const screenerURL = "https://stockanalysis.com/_api/endpoints/screener/table"

// exchangeStockCount maps exchange codes to their total stock count (cn parameter).
// This avoids unnecessary pagination by requesting the exact total in one page.
var exchangeStockCount = map[string]int{
	"ASX":    1813,
	"SHA":    2356,
	"HKG":    2753,
	"SHE":    2931,
	"NASDAQ": 3399,
}

// Screener fetches the stock list for a given exchange code.
type Screener struct {
	client *client.HTTPClient
}

// New creates a new Screener.
func New(c *client.HTTPClient) *Screener {
	return &Screener{client: c}
}

// PageResult holds the stocks from a single screener page.
type PageResult struct {
	Page       int
	Stocks     []parser.StockInfo
	TotalCount int
	IsLast     bool
}

// FetchAllStocks fetches all stocks for the given exchange code.
// Returns stocks grouped by page — export each page as a separate JSON.
func (s *Screener) FetchAllStocks(exchangeCode string) ([][]parser.StockInfo, error) {
	var allPages [][]parser.StockInfo

	page := 1
	for {
		log.Printf("[Screener] Fetching page %d for exchange %s...", page, exchangeCode)

		raw, err := s.fetchPage(exchangeCode, page)
		if err != nil {
			return nil, fmt.Errorf("fetching page %d: %w", page, err)
		}

		stocks, totalCount, err := parser.ParseScreener(raw)
		if err != nil {
			return nil, fmt.Errorf("parsing page %d: %w", page, err)
		}

		allPages = append(allPages, stocks)
		totalSoFar := 0
		for _, p := range allPages {
			totalSoFar += len(p)
		}
		log.Printf("[Screener] Page %d: got %d stocks (total so far: %d, total available: %d)",
			page, len(stocks), totalSoFar, totalCount)

		if totalSoFar >= totalCount || len(stocks) == 0 {
			break
		}

		page++
	}

	log.Printf("[Screener] Completed: %d pages for exchange %s", len(allPages), exchangeCode)
	return allPages, nil
}

func (s *Screener) fetchPage(exchangeCode string, page int) ([]byte, error) {
	// Use exchange-specific stock count; fall back to 5000 for unknown exchanges
	cn, ok := exchangeStockCount[exchangeCode]
	if !ok {
		cn = 5000
	}

	// NASDAQ uses a different filter/type format than international exchanges
	var fullURL string
	if exchangeCode == "NASDAQ" {
		fullURL = fmt.Sprintf(
			"%s?type=s&m=marketCap&s=desc&c=no,s,n,marketCap,price,change,revenue&sc=marketCap&cn=%d&f=exchange-is-%s&p=%d&i=stocks",
			screenerURL, cn, exchangeCode, page,
		)
	} else {
		fullURL = fmt.Sprintf(
			"%s?type=a&m=marketCap&s=desc&c=no,s,n,marketCap,price,change,revenue&sc=marketCap&cn=%d&f=exchangeCode-is-%s,subtype-is-stock&p=%d&i=symbols",
			screenerURL, cn, exchangeCode, page,
		)
	}

	cacheKey := fmt.Sprintf("screener/%s_p%d.json", strings.ToLower(exchangeCode), page)
	s.client.Wait() // rate limit
	log.Printf("[Screener] Fetching URL: %s", fullURL)
	return s.client.GetWithCache(fullURL, nil, cacheKey)
}
