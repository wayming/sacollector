package exporter

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"
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
// Before writing, it merges new data with any previously crawled data:
//   - Dates only in old data → preserved (keep maximum data)
//   - Dates in both old and new → new data wins (latest for same period)
//   - Dates only in new data → added
//
// Path: financials/{exchange}/{code}/{code}_{latestDate}_{statementType}.json
func (e *Exporter) ExportFinancial(exchange, code string, statements map[string]*parser.ResolvedFinancial) error {
	dir := filepath.Join(e.outputDir, "financials", strings.ToLower(exchange), code)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("creating financials output dir for %s: %w", code, err)
	}

	for stmtType, resolved := range statements {
		if resolved == nil {
			continue
		}

		newData := resolved.ToDateIndexedDict()

		// Find and merge with existing data
		mergedData := e.mergeWithExisting(dir, code, stmtType, newData)

		// Compute latest date from merged data
		latestDate := findLatestDateFromData(mergedData)
		latestFormatted := "unknown"
		if latestDate != "" {
			latestFormatted = parser.DateToUnderscore(latestDate)
		}

		output := map[string]any{
			"code":        code,
			"statement":   stmtType,
			"latest_date": latestDate,
			"data":        mergedData,
		}

		filename := fmt.Sprintf("%s_%s_%s.json", code, latestFormatted, stmtType)
		filepath := filepath.Join(dir, filename)

		if err := e.writeJSON(filepath, output); err != nil {
			return err
		}

		// Clean up old files with different filenames for the same statement type
		e.cleanupOldFiles(dir, code, stmtType, filename)
	}

	return nil
}

// mergeWithExisting reads any existing financial files for the given code+statement
// and merges the old date-indexed data with the new data.
// New data takes precedence for overlapping dates; old dates are preserved otherwise.
func (e *Exporter) mergeWithExisting(dir, code, stmtType string, newData map[string]map[string]any) map[string]map[string]any {
	// Find existing files matching {code}_*_{stmtType}.json
	pattern := fmt.Sprintf("%s_*_%s.json", code, stmtType)
	matches, err := filepath.Glob(filepath.Join(dir, pattern))
	if err != nil || len(matches) == 0 {
		// No existing data found, just return new data
		return newData
	}

	// Read the first matching existing file
	existingPath := matches[0]
	raw, err := os.ReadFile(existingPath)
	if err != nil {
		log.Printf("[Export] Unable to read existing file %s: %v — using new data only", existingPath, err)
		return newData
	}

	var existing struct {
		Data map[string]map[string]any `json:"data"`
	}
	if err := json.Unmarshal(raw, &existing); err != nil {
		log.Printf("[Export] Unable to parse existing file %s: %v — using new data only", existingPath, err)
		return newData
	}

	if existing.Data == nil {
		return newData
	}

	oldCount := len(existing.Data)
	newCount := len(newData)
	merged := mergeDateIndexedData(existing.Data, newData)
	addedCount := len(merged) - oldCount
	overlapCount := oldCount + newCount - len(merged)

	log.Printf("[Export] Merged %s/%s: %d old dates + %d new dates → %d merged (%d overlap, %d new dates added)",
		code, stmtType, oldCount, newCount, len(merged), overlapCount, addedCount)

	return merged
}

// mergeDateIndexedData merges old and new date-indexed financial data.
// New data takes precedence for overlapping date keys.
func mergeDateIndexedData(oldData, newData map[string]map[string]any) map[string]map[string]any {
	// Start with old data as the base
	merged := make(map[string]map[string]any, len(oldData)+len(newData))
	for date, metrics := range oldData {
		copied := make(map[string]any, len(metrics))
		for k, v := range metrics {
			copied[k] = v
		}
		merged[date] = copied
	}

	// Overlay new data (overwrites same date, adds new dates)
	for date, metrics := range newData {
		if existing, ok := merged[date]; ok {
			// Date exists in both: new metrics overwrite old ones for the same date
			for k, v := range metrics {
				existing[k] = v
			}
		} else {
			// New date: copy it in
			copied := make(map[string]any, len(metrics))
			for k, v := range metrics {
				copied[k] = v
			}
			merged[date] = copied
		}
	}

	return merged
}

// findLatestDateFromData returns the most recent date key from a date-indexed data map.
// Only valid date keys (YYYY-MM-DD) are considered; non-date keys like "TTM" are skipped.
func findLatestDateFromData(data map[string]map[string]any) string {
	dates := make([]string, 0, len(data))
	for dk := range data {
		if isDateKey(dk) {
			dates = append(dates, dk)
		}
	}
	if len(dates) == 0 {
		return ""
	}
	sort.Strings(dates)
	return dates[len(dates)-1]
}

// isDateKey returns true if the string looks like a date (YYYY-MM-DD).
func isDateKey(s string) bool {
	if len(s) != 10 {
		return false
	}
	if s[4] != '-' || s[7] != '-' {
		return false
	}
	for i, c := range s {
		if i == 4 || i == 7 {
			continue
		}
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}

// cleanupOldFiles removes any files matching {code}_*_{stmtType}.json that don't
// match the current filename (which has the updated latest date).
func (e *Exporter) cleanupOldFiles(dir, code, stmtType, currentFilename string) {
	pattern := filepath.Join(dir, fmt.Sprintf("%s_*_%s.json", code, stmtType))
	matches, err := filepath.Glob(pattern)
	if err != nil {
		return
	}
	for _, match := range matches {
		base := filepath.Base(match)
		if base != currentFilename {
			if err := os.Remove(match); err != nil {
				log.Printf("[Export] Warning: unable to remove old file %s: %v", match, err)
			} else {
				log.Printf("[Export] Removed old file: %s", match)
			}
		}
	}
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
