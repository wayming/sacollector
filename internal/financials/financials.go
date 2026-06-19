package financials

import (
	"fmt"
	"log"
	"sync"

	"sacollector/internal/client"
	"sacollector/internal/parser"
)

// Statement types for international exchanges (HKG, ASX, SHA, SHE).
const (
	StmtBalanceSheet    = "balance-sheet"
	StmtCashFlow        = "cash-flow-statement"
	StmtIncomeStatement = "income-statement"
	StmtRatios          = "ratios"
)

// AllStatementTypes returns all four statement types for international exchanges.
func AllStatementTypes() []string {
	return []string{StmtBalanceSheet, StmtCashFlow, StmtIncomeStatement, StmtRatios}
}

// periods to try in order of preference (quarterly → half-year → annual).
var periods = []string{"quarterly", "half-year", "annual"}

// financialsHeaders are HTTP headers required for the __data.json endpoint.
var financialsHeaders = map[string]string{
	"exp": "single",
}

// Collector fetches financial statements for stocks.
type Collector struct {
	client  *client.HTTPClient
	workers int
}

// New creates a new Collector.
func New(c *client.HTTPClient, workers int) *Collector {
	return &Collector{client: c, workers: workers}
}

// StockResult holds the financial data for a single stock.
type StockResult struct {
	Code       string
	Statements map[string]*parser.ResolvedFinancial // keyed by statement type ("metrics" for NASDAQ)
	Error      error
}

// FetchAll fetches financial statements for all given stocks.
func (c *Collector) FetchAll(stocks []parser.StockInfo, exchangeLower string) map[string]*StockResult {
	results := make(map[string]*StockResult)
	var mu sync.Mutex

	jobs := make(chan parser.StockInfo, len(stocks))
	var wg sync.WaitGroup

	for w := 0; w < c.workers; w++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()
			for stock := range jobs {
				log.Printf("[Financials] Worker %d: fetching %s (%s)...",
					workerID, stock.Code, stock.Name)

				var result *StockResult
				if exchangeLower == "nasdaq" {
					result = c.fetchOneNASDAQ(stock)
				} else {
					result = c.fetchOneInternational(stock, exchangeLower)
				}

				mu.Lock()
				results[stock.Code] = result
				mu.Unlock()
			}
		}(w)
	}

	for _, stock := range stocks {
		jobs <- stock
	}
	close(jobs)
	wg.Wait()

	return results
}

// fetchOneInternational fetches 4 statement types for HKG/ASX/SHA/SHE.
func (c *Collector) fetchOneInternational(stock parser.StockInfo, exchangeLower string) *StockResult {
	result := &StockResult{
		Code:       stock.Code,
		Statements: make(map[string]*parser.ResolvedFinancial),
	}

	// Pre-flight probe: check if this stock has financial data
	probeURL := fmt.Sprintf(
		"https://stockanalysis.com/quote/%s/%s/financials",
		exchangeLower, stock.Code,
	)
	c.client.Wait()
	if !c.client.ProbeURL(probeURL) {
		log.Printf("[Financials] %s/%s: WARNING - no financial data available, skipping", stock.Code, exchangeLower)
		result.Error = fmt.Errorf("no financial data available for %s", stock.Code)
		return result
	}

	// Try each period in order of preference, use the first that works
	usedPeriod := ""
	for _, st := range AllStatementTypes() {
		for _, pe := range periods {
			url := fmt.Sprintf(
				"https://stockanalysis.com/quote/%s/%s/financials/%s/__data.json?p=%s&x-sveltekit-trailing-slash=1&x-sveltekit-invalidated=011",
				exchangeLower, stock.Code, st, pe,
			)

			cacheKey := fmt.Sprintf("%s/%s/%s_%s.json", exchangeLower, stock.Code, st, pe)
			c.client.Wait()
			raw, err := c.client.GetWithCache(url, financialsHeaders, cacheKey)
			if err != nil {
				log.Printf("[Financials] %s/%s: %s (%s) fetch failed: %v",
					stock.Code, exchangeLower, st, pe, err)
				continue
			}

			resolved, err := parser.ParseFinancial(raw)
			if err != nil {
				log.Printf("[Financials] %s/%s: %s (%s) parse failed: %v",
					stock.Code, exchangeLower, st, pe, err)
				continue
			}

			result.Statements[st] = resolved
			if usedPeriod == "" {
				usedPeriod = resolved.Period
			}
			log.Printf("[Financials] %s/%s: %s OK [%s] (%d datekeys, period=%s)",
				stock.Code, exchangeLower, st, pe, len(resolved.DateKeys), resolved.Period)
			break // use first successful period
		}
	}

	if len(result.Statements) == 0 {
		result.Error = fmt.Errorf("all statement fetches failed for %s", stock.Code)
	}

	return result
}

// fetchOneNASDAQ fetches the single metrics endpoint for NASDAQ stocks.
func (c *Collector) fetchOneNASDAQ(stock parser.StockInfo) *StockResult {
	result := &StockResult{
		Code:       stock.Code,
		Statements: make(map[string]*parser.ResolvedFinancial),
	}

	// Pre-flight probe
	probeURL := fmt.Sprintf(
		"https://stockanalysis.com/stocks/%s/financials",
		stock.Code,
	)
	c.client.Wait()
	if !c.client.ProbeURL(probeURL) {
		log.Printf("[Financials] NASDAQ %s: WARNING - no financial data available, skipping", stock.Code)
		result.Error = fmt.Errorf("no financial data available for %s", stock.Code)
		return result
	}

	// Try each period in order of preference
	for _, pe := range periods {
		url := fmt.Sprintf(
			"https://stockanalysis.com/stocks/%s/financials/metrics/__data.json?p=%s&x-sveltekit-trailing-slash=1&x-sveltekit-invalidated=011",
			stock.Code, pe,
		)

		cacheKey := fmt.Sprintf("nasdaq/%s/metrics_%s.json", stock.Code, pe)
		c.client.Wait()
		raw, err := c.client.GetWithCache(url, financialsHeaders, cacheKey)
		if err != nil {
			log.Printf("[Financials] NASDAQ %s: metrics (%s) fetch failed: %v", stock.Code, pe, err)
			continue
		}

		resolved, err := parser.ParseFinancial(raw)
		if err != nil {
			log.Printf("[Financials] NASDAQ %s: metrics (%s) parse failed: %v", stock.Code, pe, err)
			continue
		}

		result.Statements["metrics"] = resolved
		log.Printf("[Financials] NASDAQ %s: metrics OK [%s] (%d datekeys, period=%s)",
			stock.Code, pe, len(resolved.DateKeys), resolved.Period)
		return result
	}

	return result
}
