package exporter

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"time"

	"sacollector/internal/parser"
)

// Exporter handles writing JSON output files.
type Exporter struct {
	outputDir string
}

// New creates a new Exporter with the given output directory.
func New(outputDir string) *Exporter {
	return &Exporter{outputDir: outputDir}
}

// ExportStockList writes the stock list map to a JSON file.
// Filename: {exchangeCode}_{timestamp}_p{page}.json
func (e *Exporter) ExportStockList(exchangeCode string, stocks map[string]parser.StockInfo, page int) error {
	dir := filepath.Join(e.outputDir, "stocks")
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("creating stocks output dir: %w", err)
	}

	ts := time.Now().Format("20060102_150405")
	filename := fmt.Sprintf("%s_%s_p%d.json", exchangeCode, ts, page)
	filepath := filepath.Join(dir, filename)

	return e.writeJSON(filepath, stocks)
}

// ExportFinancial writes one JSON file per statement type for a single stock.
// Filename: {code}_{latestDate}_{statementType}.json
func (e *Exporter) ExportFinancial(code string, statements map[string]*parser.ResolvedFinancial) error {
	dir := filepath.Join(e.outputDir, "financials", code)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("creating financials output dir for %s: %w", code, err)
	}

	for stmtType, resolved := range statements {
		if resolved == nil {
			continue
		}

		dateDict := resolved.ToDateIndexedDict()

		latestFormatted := resolved.LatestDateFormatted()
		if latestFormatted == "" || latestFormatted == "unknown" {
			latestFormatted = "unknown"
		}

		output := map[string]any{
			"code":        code,
			"statement":   stmtType,
			"latest_date": resolved.LatestDate(),
			"data":        dateDict,
		}

		filename := fmt.Sprintf("%s_%s_%s.json", code, latestFormatted, stmtType)
		filepath := filepath.Join(dir, filename)

		if err := e.writeJSON(filepath, output); err != nil {
			return err
		}
	}

	return nil
}

func (e *Exporter) writeJSON(path string, data any) error {
	f, err := os.Create(path)
	if err != nil {
		return fmt.Errorf("creating file %s: %w", path, err)
	}
	defer f.Close()

	encoder := json.NewEncoder(f)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(data); err != nil {
		return fmt.Errorf("encoding JSON to %s: %w", path, err)
	}

	log.Printf("[Export] Wrote %s", path)
	return nil
}
