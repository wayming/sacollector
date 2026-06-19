package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"strings"
	"time"

	"sacollector/internal/client"
	"sacollector/internal/exporter"
	"sacollector/internal/financials"
	"sacollector/internal/parser"
	"sacollector/internal/screener"
)

var validExchanges = map[string]bool{
	"ASX":    true,
	"SHA":    true,
	"HKG":    true,
	"SHE":    true,
	"NASDAQ": true,
}

func main() {
	exchange := flag.String("exchange", "HKG", "Exchange code (ASX, SHA, HKG, SHE, NASDAQ)")
	symbol := flag.String("symbol", "", "Fetch a single stock symbol (requires -exchange, skips Phase 1)")
	outputDir := flag.String("output", "./output", "Output directory for JSON files")
	limit := flag.Int("limit", 0, "Max stocks to process (0 = all)")
	workers := flag.Int("workers", 4, "Number of concurrent workers for Phase 2")
	rateLimitMs := flag.Int("rate-limit", 1000, "Rate limit between HTTP requests in ms")
	skipFinancials := flag.Bool("skip-financials", false, "Skip Phase 2 (financial statements)")
	flag.Parse()

	exchangeCode := strings.ToUpper(*exchange)
	if !validExchanges[exchangeCode] {
		fmt.Fprintf(os.Stderr, "Invalid exchange code: %s. Valid: ASX, SHA, HKG, SHE, NASDAQ\n", exchangeCode)
		os.Exit(1)
	}

	exchangeLower := strings.ToLower(exchangeCode)

	log.Printf("=== sacollector ===")
	log.Printf("Exchange: %s", exchangeCode)
	log.Printf("Output: %s", *outputDir)
	log.Printf("Workers: %d, Rate limit: %dms", *workers, *rateLimitMs)

	// Initialize shared HTTP client with raw cache
	httpClient := client.NewHTTPClient(time.Duration(*rateLimitMs) * time.Millisecond)
	httpClient.SetCacheDir(*outputDir + "/raw")
	defer httpClient.Stop()

	// Setup components
	scr := screener.New(httpClient)
	finCollector := financials.New(httpClient, *workers)
	exp := exporter.New(*outputDir)

	// ========== Phase 1: Fetch Stock List (skipped if -symbol is set) ==========
	var allStocks []parser.StockInfo

	if *symbol != "" {
		log.Printf("=== Single symbol mode: %s ===", *symbol)
		allStocks = []parser.StockInfo{{Code: *symbol, Name: *symbol}}
	} else {
		log.Printf("=== Phase 1: Fetching stock list for %s ===", exchangeCode)

		allPages, err := scr.FetchAllStocks(exchangeCode)
		if err != nil {
			log.Fatalf("Phase 1 failed: %v", err)
		}

		// Export each page as a separate JSON, flatten for Phase 2
		for i, pageStocks := range allPages {
			stockMap := parser.BuildStockMap(pageStocks)
			if err := exp.ExportStockList(exchangeCode, stockMap, i+1); err != nil {
				log.Fatalf("Exporting stock list page %d: %v", i+1, err)
			}
			allStocks = append(allStocks, pageStocks...)
		}

		log.Printf("Phase 1 complete: %d stocks across %d pages", len(allStocks), len(allPages))

		// Apply limit if specified
		if *limit > 0 && *limit < len(allStocks) {
			allStocks = allStocks[:*limit]
			log.Printf("Limited to %d stocks", *limit)
		}
	}

	// ========== Phase 2: Fetch Financial Statements ==========
	if *skipFinancials {
		log.Printf("Skipping Phase 2 (--skip-financials)")
		log.Printf("Done.")
		return
	}

	log.Printf("=== Phase 2: Fetching financial statements for %d stocks ===", len(allStocks))

	results := finCollector.FetchAll(allStocks, exchangeLower)

	successCount := 0
	failCount := 0
	for code, result := range results {
		if result.Error != nil {
			log.Printf("[%s] Failed: %v", code, result.Error)
			failCount++
			continue
		}

		if len(result.Statements) == 0 {
			log.Printf("[%s] No statements collected", code)
			failCount++
			continue
		}

		if err := exp.ExportFinancial(code, result.Statements); err != nil {
			log.Printf("[%s] Export error: %v", code, err)
			failCount++
			continue
		}

		successCount++
	}

	log.Printf("=== Done ===")
	log.Printf("Phase 1: %d stocks exported", len(allStocks))
	log.Printf("Phase 2: %d success, %d failed", successCount, failCount)
}
