package mcp

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"
)

// --- Tool 1: list_metrics ---

// ListMetricsInput is the input for the list_metrics tool.
type ListMetricsInput struct {
	Exchange string `json:"exchange" jsonschema:"Exchange code (e.g., ASX, HKG, NASDAQ, NYSE, SHA, SHE)"`
	Code     string `json:"code" jsonschema:"Stock ticker code (e.g., MGX, 0700, AAPL)"`
}

// ListMetricsOutput is the output for the list_metrics tool.
type ListMetricsOutput struct {
	Code       string              `json:"code"`
	Exchange   string              `json:"exchange"`
	Metrics    map[string][]string `json:"metrics"` // statement-type -> sorted metric names
}

// ListMetrics returns all available metrics for a stock, grouped by statement type.
func ListMetrics(reader *Reader) sdkmcp.ToolHandlerFor[ListMetricsInput, ListMetricsOutput] {
	return func(ctx context.Context, req *sdkmcp.CallToolRequest, input ListMetricsInput) (*sdkmcp.CallToolResult, ListMetricsOutput, error) {
		sd, err := reader.Load(input.Exchange, input.Code)
		if err != nil {
			return nil, ListMetricsOutput{}, fmt.Errorf("loading stock data: %w", err)
		}

		output := ListMetricsOutput{
			Code:     sd.Code,
			Exchange: sd.Exchange,
			Metrics:  make(map[string][]string),
		}

		for _, stmtType := range AllStatementTypes {
			ff, ok := sd.Files[stmtType]
			if !ok {
				continue
			}
			seen := make(map[string]bool)
			for _, metrics := range ff.Data {
				for name := range metrics {
					seen[name] = true
				}
			}
			if len(seen) > 0 {
				names := make([]string, 0, len(seen))
				for name := range seen {
					names = append(names, name)
				}
				sort.Strings(names)
				output.Metrics[stmtType] = names
			}
		}

		return nil, output, nil
	}
}

// --- Tool 2: get_data_period ---

// GetDataPeriodInput is the input for the get_data_period tool.
type GetDataPeriodInput struct {
	Exchange string `json:"exchange" jsonschema:"Exchange code (e.g., ASX, HKG, NASDAQ, NYSE, SHA, SHE)"`
	Code     string `json:"code" jsonschema:"Stock ticker code (e.g., MGX, 0700, AAPL)"`
}

// GetDataPeriodOutput is the output for the get_data_period tool.
type GetDataPeriodOutput struct {
	Code                    string   `json:"code"`
	Exchange                string   `json:"exchange"`
	EarliestDate            string   `json:"earliest_date"`
	LatestDate              string   `json:"latest_date"`
	StatementTypesAvailable []string `json:"statement_types_available"`
}

// GetDataPeriod returns the earliest and latest dates for which data exists.
func GetDataPeriod(reader *Reader) sdkmcp.ToolHandlerFor[GetDataPeriodInput, GetDataPeriodOutput] {
	return func(ctx context.Context, req *sdkmcp.CallToolRequest, input GetDataPeriodInput) (*sdkmcp.CallToolResult, GetDataPeriodOutput, error) {
		sd, err := reader.Load(input.Exchange, input.Code)
		if err != nil {
			return nil, GetDataPeriodOutput{}, fmt.Errorf("loading stock data: %w", err)
		}

		output := GetDataPeriodOutput{
			Code:     sd.Code,
			Exchange: sd.Exchange,
		}

		dateSet := make(map[string]bool)
		for stmtType, ff := range sd.Files {
			output.StatementTypesAvailable = append(output.StatementTypesAvailable, stmtType)
			for date := range ff.Data {
				if isDateKey(date) {
					dateSet[date] = true
				}
			}
		}
		sort.Strings(output.StatementTypesAvailable)

		if len(dateSet) == 0 {
			return nil, GetDataPeriodOutput{}, fmt.Errorf("no dates found for %s:%s", input.Exchange, input.Code)
		}

		dates := make([]string, 0, len(dateSet))
		for d := range dateSet {
			dates = append(dates, d)
		}
		sort.Strings(dates)

		output.EarliestDate = dates[0]
		output.LatestDate = dates[len(dates)-1]

		return nil, output, nil
	}
}

// --- Tool 3: get_financials ---

// GetFinancialsInput is the input for the get_financials tool.
type GetFinancialsInput struct {
	Exchange string   `json:"exchange" jsonschema:"Exchange code (e.g., ASX, HKG, NASDAQ, NYSE, SHA, SHE)"`
	Code     string   `json:"code" jsonschema:"Stock ticker code (e.g., MGX, 0700, AAPL)"`
	Metrics  []string `json:"metrics" jsonschema:"List of metric names to include (e.g., ['revenue', 'epsBasic', 'netinc'])"`
	Period   string   `json:"period" jsonschema:"Time period: '1y', '2y', '5y', or 'all' for all available data"`
}

// GetFinancialsOutput is the output for the get_financials tool.
type GetFinancialsOutput struct {
	Code          string                       `json:"code"`
	Exchange      string                       `json:"exchange"`
	Period        string                       `json:"period"`
	Data          map[string]map[string]any     `json:"data"`          // date -> { "statement-type": { "metric": value } }
	MetricSources map[string]string            `json:"metric_sources"` // metric -> statement-type
	Errors        []string                     `json:"errors,omitempty"`
}

// GetFinancials returns specified metrics over a given time period.
func GetFinancials(reader *Reader) sdkmcp.ToolHandlerFor[GetFinancialsInput, GetFinancialsOutput] {
	return func(ctx context.Context, req *sdkmcp.CallToolRequest, input GetFinancialsInput) (*sdkmcp.CallToolResult, GetFinancialsOutput, error) {
		sd, err := reader.Load(input.Exchange, input.Code)
		if err != nil {
			return nil, GetFinancialsOutput{}, fmt.Errorf("loading stock data: %w", err)
		}

		// Validate period
		if input.Period != "1y" && input.Period != "2y" && input.Period != "5y" && input.Period != "all" {
			return nil, GetFinancialsOutput{}, fmt.Errorf("invalid period %q: must be '1y', '2y', '5y', or 'all'", input.Period)
		}

		output := GetFinancialsOutput{
			Code:          sd.Code,
			Exchange:      sd.Exchange,
			Period:        input.Period,
			Data:          make(map[string]map[string]any),
			MetricSources: make(map[string]string),
		}

		// Build metric -> statement type lookup
		metricStmt := buildMetricIndex(sd, input.Metrics)
		for _, metric := range input.Metrics {
			if stmt, ok := metricStmt[metric]; ok {
				output.MetricSources[metric] = stmt
			}
		}

		// Compute cutoff date
		cutoff := computeCutoff(sd, input.Period)

		// Collect all dates across relevant statement files
		dateSet := make(map[string]bool)
		seenStmts := make(map[string]bool)
		for _, stmtType := range metricStmt {
			seenStmts[stmtType] = true
		}
		for stmtType := range seenStmts {
			ff := sd.Files[stmtType]
			if ff == nil {
				continue
			}
			for date := range ff.Data {
				if isDateKey(date) && (cutoff == nil || date >= *cutoff) {
					dateSet[date] = true
				}
			}
		}

		dates := make([]string, 0, len(dateSet))
		for d := range dateSet {
			dates = append(dates, d)
		}
		sort.Strings(dates)

		// Build output for each date
		for _, date := range dates {
			dateEntry := make(map[string]any)

			for _, metric := range input.Metrics {
				stmtType, ok := metricStmt[metric]
				if !ok {
					continue
				}
				ff := sd.Files[stmtType]
				if ff == nil {
					continue
				}
				if dateMetrics, ok := ff.Data[date]; ok {
					if val, ok := dateMetrics[metric]; ok {
						if _, exists := dateEntry[stmtType]; !exists {
							dateEntry[stmtType] = make(map[string]any)
						}
						dateEntry[stmtType].(map[string]any)[metric] = val
					}
				}
			}

			// Only include dates that have data for at least one requested metric
			for _, stmtMap := range dateEntry {
				if len(stmtMap.(map[string]any)) > 0 {
					output.Data[date] = dateEntry
					break
				}
			}
		}

		// Collect missing metrics
		for _, metric := range input.Metrics {
			if _, ok := metricStmt[metric]; !ok {
				output.Errors = append(output.Errors, fmt.Sprintf("metric %q not found in any statement", metric))
			}
		}

		return nil, output, nil
	}
}

// buildMetricIndex finds which statement type each requested metric belongs to.
func buildMetricIndex(sd *StockData, metrics []string) map[string]string {
	result := make(map[string]string)
	for _, metric := range metrics {
		// Check if it's a prefixed metric like "income-statement.revenue"
		parts := strings.SplitN(metric, ".", 2)
		if len(parts) == 2 {
			// Explicit statement prefix
			stmtType := parts[0]
			metricName := parts[1]
			if ff, ok := sd.Files[stmtType]; ok {
				for _, m := range ff.Data {
					if _, exists := m[metricName]; exists {
						result[metric] = stmtType
						break
					}
				}
			}
		} else {
			// Auto-detect: search across all statement types
			for _, stmtType := range AllStatementTypes {
				ff, ok := sd.Files[stmtType]
				if !ok {
					continue
				}
				found := false
				for _, m := range ff.Data {
					if _, exists := m[metric]; exists {
						result[metric] = stmtType
						found = true
						break
					}
				}
				if found {
					break
				}
			}
		}
	}
	return result
}

// isDateKey returns true if the string looks like a date (YYYY-MM-DD).
// Filters out non-date keys like "TTM" (trailing twelve months).
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

// computeCutoff returns the cutoff date string based on the period.
// Returns nil for "all".
func computeCutoff(sd *StockData, period string) *string {
	if period == "all" {
		return nil
	}

	// Find the latest date across all files
	var latestDate string
	for _, ff := range sd.Files {
		if isDateKey(ff.LatestDate) && ff.LatestDate > latestDate {
			latestDate = ff.LatestDate
		}
	}
	if latestDate == "" {
		// Fallback: find max date key from data
		for _, ff := range sd.Files {
			for date := range ff.Data {
				if isDateKey(date) && date > latestDate {
					latestDate = date
				}
			}
		}
	}
	if latestDate == "" {
		return nil
	}

	t, err := time.Parse("2006-01-02", latestDate)
	if err != nil {
		return nil
	}

	var years int
	switch period {
	case "1y":
		years = 1
	case "2y":
		years = 2
	case "5y":
		years = 5
	default:
		return nil
	}

	cutoff := t.AddDate(-years, 0, 0).Format("2006-01-02")
	return &cutoff
}
