package mcp

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
)

// Statement types known to the system.
const (
	StmtIncomeStatement    = "income-statement"
	StmtBalanceSheet       = "balance-sheet"
	StmtCashFlowStatement  = "cash-flow-statement"
	StmtRatios             = "ratios"
)

// AllStatementTypes lists the four statement types in canonical order.
var AllStatementTypes = []string{StmtIncomeStatement, StmtBalanceSheet, StmtCashFlowStatement, StmtRatios}

// FinancialFile holds the parsed content of one exported JSON file.
type FinancialFile struct {
	Code       string                    `json:"code"`
	Statement  string                    `json:"statement"`
	LatestDate string                    `json:"latest_date"`
	Data       map[string]map[string]any `json:"data"`
}

// StockData holds all statement files for a single stock.
type StockData struct {
	Code    string
	Exchange string
	Files   map[string]*FinancialFile // keyed by statement type
}

// Reader loads financial JSON from the output directory.
type Reader struct {
	OutputDir string
	cache     sync.Map // key: "exchange:code" -> *StockData
}

// NewReader creates a new Reader.
func NewReader(outputDir string) *Reader {
	return &Reader{OutputDir: outputDir}
}

// Load reads all financial JSON files for a given stock from disk.
// Returns nil if the stock directory does not exist or has no files.
func (r *Reader) Load(exchange, code string) (*StockData, error) {
	key := strings.ToLower(exchange) + ":" + code
	if cached, ok := r.cache.Load(key); ok {
		return cached.(*StockData), nil
	}

	dir := filepath.Join(r.OutputDir, "financials", strings.ToLower(exchange), code)
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("no data found for %s:%s", exchange, code)
		}
		return nil, fmt.Errorf("reading directory %s: %w", dir, err)
	}

	sd := &StockData{
		Code:     code,
		Exchange: strings.ToLower(exchange),
		Files:    make(map[string]*FinancialFile),
	}

	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}

		stmtType := detectStatementType(entry.Name())
		if stmtType == "" {
			continue
		}

		raw, err := os.ReadFile(filepath.Join(dir, entry.Name()))
		if err != nil {
			log.Printf("[mcp] warning: failed to read %s: %v", entry.Name(), err)
			continue
		}

		var ff FinancialFile
		if err := json.Unmarshal(raw, &ff); err != nil {
			log.Printf("[mcp] warning: failed to parse %s: %v", entry.Name(), err)
			continue
		}

		sd.Files[stmtType] = &ff
	}

	if len(sd.Files) == 0 {
		return nil, fmt.Errorf("no financial data files found for %s:%s", exchange, code)
	}

	r.cache.Store(key, sd)
	return sd, nil
}

// detectStatementType extracts the statement type from a filename.
// Filename pattern: {code}_{YYYY}_{MM}_{DD}_{statement-type}.json
func detectStatementType(filename string) string {
	for _, st := range AllStatementTypes {
		if strings.Contains(filename, st) {
			return st
		}
	}
	return ""
}

// AvailableExchanges returns the list of exchange codes that have data.
func (r *Reader) AvailableExchanges() []string {
	dir := filepath.Join(r.OutputDir, "financials")
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var exchanges []string
	for _, e := range entries {
		if e.IsDir() {
			exchanges = append(exchanges, strings.ToUpper(e.Name()))
		}
	}
	sort.Strings(exchanges)
	return exchanges
}

// ListAvailableStocks returns all stocks available for a given exchange.
func (r *Reader) ListAvailableStocks(exchange string) ([]string, error) {
	dir := filepath.Join(r.OutputDir, "financials", strings.ToLower(exchange))
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("reading exchange dir %s: %w", dir, err)
	}
	var codes []string
	for _, e := range entries {
		if e.IsDir() {
			codes = append(codes, e.Name())
		}
	}
	sort.Strings(codes)
	return codes, nil
}
