package api

import (
	"fmt"
	"net/http"
	"strings"

	mcplib "sacollector/internal/mcp"
)

// handleMcpListMetrics handles GET /api/mcp/list-metrics?exchange=X&code=Y
func (s *Server) handleMcpListMetrics(w http.ResponseWriter, r *http.Request) {
	exchange := r.URL.Query().Get("exchange")
	code := r.URL.Query().Get("code")
	if exchange == "" || code == "" {
		http.Error(w, "need exchange and code", http.StatusBadRequest)
		return
	}

	reader := mcplib.NewReader(s.OutputDir)
	sd, err := reader.Load(exchange, code)
	if err != nil {
		writeJSON(w, map[string]interface{}{"error": err.Error()})
		return
	}

	result := map[string][]string{}
	for _, stmtType := range mcplib.AllStatementTypes {
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
			result[stmtType] = names
		}
	}

	writeJSON(w, map[string]interface{}{
		"code":     sd.Code,
		"exchange": sd.Exchange,
		"metrics":  result,
	})
}

// handleMcpDataPeriod handles GET /api/mcp/data-period?exchange=X&code=Y
func (s *Server) handleMcpDataPeriod(w http.ResponseWriter, r *http.Request) {
	exchange := r.URL.Query().Get("exchange")
	code := r.URL.Query().Get("code")
	if exchange == "" || code == "" {
		http.Error(w, "need exchange and code", http.StatusBadRequest)
		return
	}

	reader := mcplib.NewReader(s.OutputDir)
	sd, err := reader.Load(exchange, code)
	if err != nil {
		writeJSON(w, map[string]interface{}{"error": err.Error()})
		return
	}

	dateSet := make(map[string]bool)
	stmtTypes := []string{}
	for stmtType, ff := range sd.Files {
		stmtTypes = append(stmtTypes, stmtType)
		for date := range ff.Data {
			if isDateKey(date) {
				dateSet[date] = true
			}
		}
	}

	if len(dateSet) == 0 {
		writeJSON(w, map[string]interface{}{"error": "no dates found"})
		return
	}

	dates := make([]string, 0, len(dateSet))
	for d := range dateSet {
		dates = append(dates, d)
	}
	sortDates(dates)

	writeJSON(w, map[string]interface{}{
		"code":                      sd.Code,
		"exchange":                  sd.Exchange,
		"earliest_date":             dates[0],
		"latest_date":               dates[len(dates)-1],
		"statement_types_available": stmtTypes,
	})
}

// handleMcpFinancials handles GET /api/mcp/financials?exchange=X&code=Y&metrics=a,b,c&period=2y
func (s *Server) handleMcpFinancials(w http.ResponseWriter, r *http.Request) {
	exchange := r.URL.Query().Get("exchange")
	code := r.URL.Query().Get("code")
	metricsStr := r.URL.Query().Get("metrics")
	period := r.URL.Query().Get("period")
	if exchange == "" || code == "" || metricsStr == "" {
		http.Error(w, "need exchange, code, and metrics", http.StatusBadRequest)
		return
	}
	if period == "" {
		period = "all"
	}
	if period != "1y" && period != "2y" && period != "5y" && period != "all" {
		http.Error(w, "period must be 1y, 2y, 5y, or all", http.StatusBadRequest)
		return
	}

	metrics := strings.Split(metricsStr, ",")
	for i := range metrics {
		metrics[i] = strings.TrimSpace(metrics[i])
	}

	reader := mcplib.NewReader(s.OutputDir)
	sd, err := reader.Load(exchange, code)
	if err != nil {
		writeJSON(w, map[string]interface{}{"error": err.Error()})
		return
	}

	// Build metric -> statement type lookup
	metricStmt := buildMetricIndexHTTP(sd, metrics)
	metricSources := map[string]string{}
	for _, m := range metrics {
		if st, ok := metricStmt[m]; ok {
			metricSources[m] = st
		}
	}

	// Compute cutoff
	cutoff := computeCutoffHTTP(sd, period)

	// Collect dates
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
	sortDates(dates)

	// Build output
	data := map[string]map[string]interface{}{}
	for _, date := range dates {
		dateEntry := map[string]interface{}{}
		for _, metric := range metrics {
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
						dateEntry[stmtType] = map[string]interface{}{}
					}
					dateEntry[stmtType].(map[string]interface{})[metric] = val
				}
			}
		}
		if len(dateEntry) > 0 {
			data[date] = dateEntry
		}
	}

	// Collect missing metrics
	errs := []string{}
	for _, metric := range metrics {
		if _, ok := metricStmt[metric]; !ok {
			errs = append(errs, fmt.Sprintf("metric %q not found in any statement", metric))
		}
	}

	result := map[string]interface{}{
		"code":           sd.Code,
		"exchange":       sd.Exchange,
		"period":         period,
		"data":           data,
		"metric_sources": metricSources,
	}
	if len(errs) > 0 {
		result["errors"] = errs
	}

	writeJSON(w, result)
}

// Helpers shared with the MCP package (duplicated to avoid coupling).

func isDateKey(s string) bool {
	if len(s) != 10 || s[4] != '-' || s[7] != '-' {
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

func sortDates(dates []string) {
	for i := 0; i < len(dates); i++ {
		for j := i + 1; j < len(dates); j++ {
			if dates[i] > dates[j] {
				dates[i], dates[j] = dates[j], dates[i]
			}
		}
	}
}

func buildMetricIndexHTTP(sd *mcplib.StockData, metrics []string) map[string]string {
	result := make(map[string]string)
	for _, metric := range metrics {
		parts := splitMetric(metric)
		if len(parts) == 2 {
			stmtType, metricName := parts[0], parts[1]
			if ff, ok := sd.Files[stmtType]; ok {
				for _, m := range ff.Data {
					if _, exists := m[metricName]; exists {
						result[metric] = stmtType
						break
					}
				}
			}
		} else {
			for _, stmtType := range mcplib.AllStatementTypes {
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

func splitMetric(metric string) []string {
	idx := strings.Index(metric, ".")
	if idx < 0 {
		return []string{metric}
	}
	return []string{metric[:idx], metric[idx+1:]}
}

func computeCutoffHTTP(sd *mcplib.StockData, period string) *string {
	if period == "all" {
		return nil
	}

	var latestDate string
	for _, ff := range sd.Files {
		if isDateKey(ff.LatestDate) && ff.LatestDate > latestDate {
			latestDate = ff.LatestDate
		}
	}
	if latestDate == "" {
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

	// Parse YYYY-MM-DD
	y, m, d := 0, 0, 0
	fmt.Sscanf(latestDate, "%d-%d-%d", &y, &m, &d)

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

	cutoff := fmt.Sprintf("%04d-%02d-%02d", y-years, m, d)
	return &cutoff
}
