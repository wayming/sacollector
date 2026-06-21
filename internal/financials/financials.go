package financials

import (
	"context"
	"fmt"
	"strings"
	"log"
	"sync"
	"time"

	"sacollector/internal/client"
	"sacollector/internal/exporter"
	"sacollector/internal/parser"
	"sacollector/internal/store"
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
				result = c.fetchOneInternational(context.Background(), stock, exchangeLower)

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

// FetchFromQueue processes stocks from a Redis-backed queue.
func (c *Collector) FetchFromQueue(redisStore *store.RedisStore, exchangeLower string, exp *exporter.Exporter) {
	c.FetchFromQueueWithContext(context.Background(), redisStore, exchangeLower, exp)
}

// FetchFromQueueWithContext processes stocks with context cancellation support.
func (c *Collector) FetchFromQueueWithContext(ctx context.Context, redisStore *store.RedisStore, exchangeLower string, exp *exporter.Exporter) {
	log.Printf("[Financials] Starting %d workers for %s", c.workers, exchangeLower)

	// Periodic status reporter
	done := make(chan struct{})
	defer close(done)
	go func() {
		tick := time.NewTicker(5 * time.Second)
		defer tick.Stop()
		for {
			select {
			case <-tick.C:
				d, t := redisStore.Progress()
				log.Printf("[STATUS] %d/%d done, %d pending, %d active HTTP",
					d, t, redisStore.GetRemainingQueue(), c.client.Active())
			case <-done:
				return
			}
		}
	}()

	var wg sync.WaitGroup

	for w := 0; w < c.workers; w++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()
			for {
				select {
				case <-ctx.Done():
					return
				default:
				}

				stock, ok := redisStore.Dequeue(2 * time.Second)
				if !ok {
					// Check if ctx cancelled before giving up
					select {
					case <-ctx.Done():
						return
					default:
						// Check if queue is truly empty
						if redisStore.GetRemainingQueue() == 0 {
							return
						}
						continue
					}
				}

				done, total := redisStore.Progress()
				tStart := time.Now()
				log.Printf("[Financials] Worker %d: fetching %s (%s)... [%d/%d]",
					workerID, stock.Code, stock.Name, done, total)

				var result *StockResult
				result = c.fetchOneInternational(ctx, stock, exchangeLower)

				elapsed := time.Since(tStart).Milliseconds()
				if result.Error != nil || len(result.Statements) == 0 {
					errMsg := "unknown error"
					if result.Error != nil {
						errMsg = result.Error.Error()
					}
					log.Printf("[%s] Error (%dms): %s", stock.Code, elapsed, errMsg)

					if isServerError(errMsg) {
						redisStore.Requeue(stock.Code, errMsg)
					} else {
						redisStore.MarkFailed(stock.Code, errMsg)
					}
				} else {
					if err := exp.ExportFinancial(exchangeLower, stock.Code, result.Statements); err != nil {
						log.Printf("[%s] Export error: %v", stock.Code, err)
						redisStore.MarkFailed(stock.Code, err.Error())
					} else {
						log.Printf("[%s] Done (%dms)", stock.Code, elapsed)
						redisStore.MarkDone(stock.Code)
					}
				}
			}
		}(w)
	}

	wg.Wait()

	// Print error summary at the end
	if errs := redisStore.GetErrors(); len(errs) > 0 {
		log.Printf("[Financials] %d stocks failed:", len(errs))
		for _, e := range errs {
			log.Printf("  %s", e)
		}
	}
}

// isServerError returns true for transient errors that should be retried.
func isServerError(errMsg string) bool {
	// HTTP 5xx, timeouts, connection issues → retry
	markers := []string{"HTTP 5", "timeout", "connection refused", "connection reset", "EOF", "TLS handshake"}
	for _, m := range markers {
		if len(errMsg) > len(m) && containsStr(errMsg, m) {
			return true
		}
	}
	return false
}

func containsStr(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

// fetchOneInternational fetches 4 statement types for HKG/ASX/SHA/SHE.
func (c *Collector) fetchOneInternational(ctx context.Context, stock parser.StockInfo, exchangeLower string) *StockResult {
	result := &StockResult{
		Code:       stock.Code,
		Statements: make(map[string]*parser.ResolvedFinancial),
	}

	// Try each period in order of preference, use the first that works
	var failReasons []string
	usedPeriod := ""
	for _, st := range AllStatementTypes() {
		stmtOK := false
		for _, pe := range periods {
			var url string
			if exchangeLower == "nasdaq" || exchangeLower == "nyse" {
				url = fmt.Sprintf(
					"https://stockanalysis.com/stocks/%s/financials/%s/__data.json?p=%s&x-sveltekit-trailing-slash=1&x-sveltekit-invalidated=011",
					stock.Code, st, pe,
				)
			} else {
				url = fmt.Sprintf(
					"https://stockanalysis.com/quote/%s/%s/financials/%s/__data.json?p=%s&x-sveltekit-trailing-slash=1&x-sveltekit-invalidated=011",
					exchangeLower, stock.Code, st, pe,
				)
			}
			cacheKey := fmt.Sprintf("%s/%s/%s_%s.json", exchangeLower, stock.Code, st, pe)
			t0 := time.Now()
			c.client.Wait()
			raw, err := c.client.GetWithCacheContext(ctx, url, financialsHeaders, cacheKey)
			tFetch := time.Since(t0).Milliseconds()
			if err != nil {
				failReasons = append(failReasons, fmt.Sprintf("%s(%s) fetch=%dms: %v", st, pe, tFetch, err))
				continue
			}

			t1 := time.Now()
			resolved, err := parser.ParseFinancial(raw)
			tParse := time.Since(t1).Milliseconds()
			if err != nil {
				failReasons = append(failReasons, fmt.Sprintf("%s(%s) parse=%dms: %v", st, pe, tParse, err))
				continue
			}

			result.Statements[st] = resolved
			stmtOK = true
			if usedPeriod == "" {
				usedPeriod = resolved.Period
			}
			log.Printf("[Financials] %s/%s: %s OK fetch=%dms parse=%dms (%d datekeys)",
				stock.Code, exchangeLower, st, tFetch, tParse, len(resolved.DateKeys))
			break
		}
		if !stmtOK {
			// If first statement fails with error node, stock has no data — skip rest
			for _, r := range failReasons {
				if len(r) > 10 && r[10:] == "error node" || strings.Contains(r, "error node") {
					log.Printf("[Financials] %s/%s: no financial data (error node), skipping remaining", stock.Code, exchangeLower)
					result.Error = fmt.Errorf("no financial data for %s", stock.Code)
					return result
				}
			}
		}
	}

	if len(result.Statements) == 0 {
		result.Error = fmt.Errorf("all statement fetches failed for %s: %v", stock.Code, failReasons)
	}

	return result
}


