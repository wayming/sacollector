package parser

import (
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
)

// FinancialResponse represents the top-level JSON response from the __data.json endpoint.
type FinancialResponse struct {
	Type  string           `json:"type"`
	Nodes []FinancialNode  `json:"nodes"`
}

// FinancialNode represents a node in the response.
type FinancialNode struct {
	Type string `json:"type"`
	Data []any  `json:"data"`
}

// ResolvedFinancial holds the fully resolved financial data.
type ResolvedFinancial struct {
	Statement       string
	Period          string
	Title           string
	Description     string
	Source          string
	FiscalYear      string
	LastTrailDate   string
	FiscalYears     []int
	FiscalQuarters  []string
	DateKeys        []string
	Financials      map[string]any
	FieldLabels     map[string]string
}

// ParseFinancial parses the raw JSON bytes from a __data.json endpoint.
// Returns a ResolvedFinancial with data keyed by date.
func ParseFinancial(raw []byte) (*ResolvedFinancial, error) {
	var resp FinancialResponse
	if err := json.Unmarshal(raw, &resp); err != nil {
		return nil, fmt.Errorf("unmarshaling financial response: %w", err)
	}

	// Check for error nodes first
	for _, node := range resp.Nodes {
		if node.Type == "error" {
			return nil, fmt.Errorf("API returned error node")
		}
	}

	// Find the node with type "data" that has a financialData schema
	var dataArr []any
	for _, node := range resp.Nodes {
		if node.Type != "data" || len(node.Data) < 10 {
			continue
		}
		// Verify the schema has expected financial keys
		if schema, ok := node.Data[0].(map[string]any); ok {
			if _, hasFD := schema["financialData"]; hasFD {
				dataArr = node.Data
				break
			}
		}
	}
	if dataArr == nil {
		return nil, fmt.Errorf("no financial data node found in response")
	}

	// Index 0 is the schema
	schema, ok := dataArr[0].(map[string]any)
	if !ok {
		return nil, fmt.Errorf("schema (data[0]) is not a map")
	}

	resolved := resolveDict(dataArr, schema, 0)
	if resolved == nil {
		return nil, fmt.Errorf("failed to resolve financial data")
	}

	result := &ResolvedFinancial{}

	// Extract top-level fields
	if v, ok := resolved["statement"]; ok {
		result.Statement = toString(v)
	}
	if v, ok := resolved["period"]; ok {
		result.Period = toString(v)
	}
	if v, ok := resolved["source"]; ok {
		result.Source = toString(v)
	}

	// Head
	if head, ok := resolved["head"].(map[string]any); ok {
		result.Title = toString(head["heading"])
		result.Description = toString(head["description"])
	}

	// Details
	if details, ok := resolved["details"].(map[string]any); ok {
		result.FiscalYear = toString(details["fiscalYear"])
		result.LastTrailDate = toString(details["lastTrailingDate"])
	}

	// Financial data
	if fd, ok := resolved["financialData"].(map[string]any); ok {
		result.DateKeys = toStringSlice(fd["datekey"])
		result.FiscalYears = toIntSlice(fd["fiscalYear"])
		result.FiscalQuarters = toStringSlice(fd["fiscalQuarter"])


		result.Financials = make(map[string]any)
		for key, val := range fd {
			if key == "datekey" || key == "fiscalYear" || key == "fiscalQuarter" {
				continue
			}
			result.Financials[key] = val
		}
	}

	// Field labels (map)
	if m, ok := resolved["map"].([]any); ok {
		result.FieldLabels = make(map[string]string)
		for _, item := range m {
			if entry, ok := item.(map[string]any); ok {
				id := toString(entry["id"])
				title := toString(entry["title"])
				if id != "" {
					result.FieldLabels[id] = title
				}
			}
		}
	}

	return result, nil
}

// ToDateIndexedDict converts the financial data into a date-keyed structure.
// Handles both international flat-metrics format and NASDAQ categories format.
// Output format: map[date]map[metricName]value
func (r *ResolvedFinancial) ToDateIndexedDict() map[string]map[string]any {
	// Check if this is the NASDAQ categories format
	if cats, ok := r.Financials["categories"]; ok {
		return r.toDateIndexedDictCategories(cats)
	}

	// International flat-metrics format
	return r.toDateIndexedDictFlat()
}

func (r *ResolvedFinancial) toDateIndexedDictFlat() map[string]map[string]any {
	result := make(map[string]map[string]any)

	for i, dateKey := range r.DateKeys {
		if dateKey == "" {
			continue
		}

		metrics := make(map[string]any)

		// Gather all financial metrics for this date index
			for metricName, val := range r.Financials {
			if metricName == "categoryNames" || metricName == "metricOrderByCategory" || metricName == "categories" {
				continue
			}
			if arr, ok := val.([]any); ok && i < len(arr) {
				metrics[metricName] = arr[i]
			}
		}

		if len(metrics) > 0 {
			result[dateKey] = metrics
		}
	}

	return result
}

// toDateIndexedDictCategories handles the NASDAQ-style categories format.
// Structure: categories = map[categoryName]map[metricName][]value
func (r *ResolvedFinancial) toDateIndexedDictCategories(cats any) map[string]map[string]any {
	result := make(map[string]map[string]any)

	categories, ok := cats.(map[string]any)
	if !ok {
		return result
	}

	for categoryName, categoryData := range categories {
		catMetrics, ok := categoryData.(map[string]any)
		if !ok {
			continue
		}

		for metricName, valuesRaw := range catMetrics {
			values, ok := valuesRaw.([]any)
			if !ok {
				continue
			}

			for i, val := range values {
				if i >= len(r.DateKeys) {
					break
				}
				dateKey := r.DateKeys[i]
				if dateKey == "" {
					continue
				}

				if _, exists := result[dateKey]; !exists {
					result[dateKey] = make(map[string]any)
				}

				// Key format: category.metricName
				key := categoryName + "." + metricName
				result[dateKey][key] = val
			}
		}
	}

	return result
}

// LatestDate returns the most recent date from the date keys.
// Non-date entries (like "TTM") are filtered out.
func (r *ResolvedFinancial) LatestDate() string {
	if len(r.DateKeys) == 0 {
		return ""
	}

	// Collect only valid date strings (YYYY-MM-DD format)
	dates := make([]string, 0, len(r.DateKeys))
	for _, dk := range r.DateKeys {
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

// LatestDateFormatted returns the latest date as YYYY_MM_DD.
func (r *ResolvedFinancial) LatestDateFormatted() string {
	latest := r.LatestDate()
	if latest == "" {
		return "unknown"
	}
	// Convert "2026-03-31" to "2026_03_31"
	return DateToUnderscore(latest)
}

// MergeMetrics creates a combined map of all financial data for a stock
// across all four statement types, keyed by date.
func MergeMetrics(statements map[string]*ResolvedFinancial) map[string]map[string]any {
	combined := make(map[string]map[string]any)

	for stmtType, resolved := range statements {
		dateDict := resolved.ToDateIndexedDict()
		for date, metrics := range dateDict {
			if _, exists := combined[date]; !exists {
				combined[date] = make(map[string]any)
			}
			for metricName, value := range metrics {
				// Prefix metric with statement type to avoid collisions
				key := stmtType + "." + metricName
				combined[date][key] = value
			}
		}
	}

	return combined
}

// --- recursive resolver (ported from parse_stock_json.py) ---

const maxDepth = 100

func resolveDict(data []any, schema map[string]any, depth int) map[string]any {
	if depth > maxDepth {
		return schema
	}

	result := make(map[string]any)
	for key, idx := range schema {
		i, ok := toInt(idx)
		if !ok || i < 0 || i >= len(data) {
			result[key] = idx
			continue
		}

		val := data[i]

		// nil → nil
		if val == nil {
			result[key] = nil
			continue
		}

		// If value is a map where all values are integers, it's a sub-schema
		if subMap, ok := val.(map[string]any); ok && isAllIntValues(subMap) {
			result[key] = resolveDict(data, subMap, depth+1)
			continue
		}

		// Otherwise resolve the value
		result[key] = resolveVal(data, val, depth+1)
	}

	return result
}

func resolveVal(data []any, val any, depth int) any {
	if depth > maxDepth {
		return val
	}

	switch v := val.(type) {
	case float64:
		// float64 can represent either int or float
		if v == float64(int64(v)) {
			i := int(v)
			if i >= 0 && i < len(data) {
				inner := data[i]
				if inner == nil {
					return nil
				}
				if isSimpleValue(inner) {
					return inner
				}
				if subMap, ok := inner.(map[string]any); ok && isAllIntValues(subMap) {
					return resolveDict(data, subMap, depth+1)
				}
				if innerList, ok := inner.([]any); ok {
					return resolveList(data, innerList, depth+1)
				}
				return inner
			}
			return v
		}
		return v

	case []any:
		return resolveList(data, v, depth+1)

	case map[string]any:
		if isAllIntValues(v) {
			return resolveDict(data, v, depth+1)
		}
		// Resolve each value in the map
		resolved := make(map[string]any)
		for mk, mv := range v {
			resolved[mk] = resolveVal(data, mv, depth+1)
		}
		return resolved

	default:
		return val
	}
}

func resolveList(data []any, list []any, depth int) []any {
	result := make([]any, len(list))
	for i, item := range list {
		result[i] = resolveVal(data, item, depth+1)
	}
	return result
}

func isAllIntValues(m map[string]any) bool {
	if len(m) == 0 {
		return false
	}
	for _, v := range m {
		if _, ok := toInt(v); !ok {
			return false
		}
	}
	return true
}

func isSimpleValue(v any) bool {
	switch v.(type) {
	case float64, string, bool:
		return true
	}
	return false
}

// --- helpers ---

func toInt(v any) (int, bool) {
	switch val := v.(type) {
	case float64:
		return int(val), true
	case int:
		return val, true
	case json.Number:
		i, err := val.Int64()
		if err != nil {
			return 0, false
		}
		return int(i), true
	}
	return 0, false
}

func toString(v any) string {
	if v == nil {
		return ""
	}
	switch val := v.(type) {
	case string:
		return val
	case float64:
		if val == float64(int64(val)) {
			return strconv.FormatInt(int64(val), 10)
		}
		return strconv.FormatFloat(val, 'f', -1, 64)
	case json.Number:
		return val.String()
	default:
		return fmt.Sprintf("%v", val)
	}
}

func toStringSlice(v any) []string {
	if v == nil {
		return nil
	}
	arr, ok := v.([]any)
	if !ok {
		return nil
	}
	result := make([]string, len(arr))
	for i, item := range arr {
		result[i] = toString(item)
	}
	return result
}

func toIntSlice(v any) []int {
	if v == nil {
		return nil
	}
	arr, ok := v.([]any)
	if !ok {
		return nil
	}
	result := make([]int, len(arr))
	for i, item := range arr {
		val, _ := toInt(item)
		result[i] = val
	}
	return result
}

// DateToUnderscore converts "2026-03-31" → "2026_03_31".
func DateToUnderscore(date string) string {
	// "2026-03-31" → "2026_03_31"
	result := make([]byte, len(date))
	for i := 0; i < len(date); i++ {
		if date[i] == '-' {
			result[i] = '_'
		} else {
			result[i] = date[i]
		}
	}
	return string(result)
}
