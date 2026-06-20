package parser

import (
	"bytes"
	"fmt"
	"strings"

	"golang.org/x/net/html"
)

// ParseExportHTML parses the HTML table from financials-export endpoint.
// Returns a ResolvedFinancial with date-keyed data, compatible with __data.json parser output.
func ParseExportHTML(raw []byte, statementType string) (*ResolvedFinancial, error) {
	doc, err := html.Parse(bytes.NewReader(raw))
	if err != nil {
		return nil, fmt.Errorf("parsing HTML: %w", err)
	}

	table := findTable(doc)
	if table == nil {
		return nil, fmt.Errorf("no table found in export HTML")
	}

	rows := findRows(table)
	if len(rows) < 2 {
		return nil, fmt.Errorf("table has < 2 rows")
	}

	// First row: date headers (skip first cell which is metric label)
	dates := extractDates(rows[0])
	if len(dates) == 0 {
		return nil, fmt.Errorf("no date headers found")
	}

	// Parse metric rows
	financials := make(map[string]any)
	for _, row := range rows[1:] {
		name, values := parseRow(row, len(dates))
		if name == "" {
			continue
		}
		arr := make([]any, len(values))
		for i, v := range values {
			arr[i] = v
		}
		financials[name] = arr
	}

	result := &ResolvedFinancial{
		Statement:  statementType,
		Period:     "quarterly",
		DateKeys:   dates,
		Financials: financials,
	}
	return result, nil
}

func findTable(n *html.Node) *html.Node {
	if n.Type == html.ElementNode && n.Data == "table" {
		return n
	}
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		if t := findTable(c); t != nil {
			return t
		}
	}
	return nil
}

func findRows(table *html.Node) []*html.Node {
	var tbody *html.Node
	for c := table.FirstChild; c != nil; c = c.NextSibling {
		if c.Type == html.ElementNode && c.Data == "tbody" {
			tbody = c
			break
		}
	}
	if tbody == nil {
		tbody = table
	}
	var rows []*html.Node
	for c := tbody.FirstChild; c != nil; c = c.NextSibling {
		if c.Type == html.ElementNode && c.Data == "tr" {
			rows = append(rows, c)
		}
	}
	return rows
}

func extractDates(row *html.Node) []string {
	var dates []string
	for c := row.FirstChild; c != nil; c = c.NextSibling {
		if c.Type == html.ElementNode && (c.Data == "th" || c.Data == "td") {
			text := strings.TrimSpace(getText(c))
			if text != "" && text != "Period" && text != "Quarter" {
				dates = append(dates, text)
			}
		}
	}
	return dates
}

func parseRow(row *html.Node, numDates int) (string, []float64) {
	var cells []string
	for c := row.FirstChild; c != nil; c = c.NextSibling {
		if c.Type == html.ElementNode && (c.Data == "th" || c.Data == "td") {
			cells = append(cells, strings.TrimSpace(getText(c)))
		}
	}
	if len(cells) < 2 {
		return "", nil
	}
	name := cells[0]
	values := make([]float64, 0, numDates)
	for i := 1; i < len(cells) && i <= numDates; i++ {
		v := parseNumber(cells[i])
		values = append(values, v)
	}
	return sanitizeMetricName(name), values
}

func getText(n *html.Node) string {
	var buf strings.Builder
	var f func(*html.Node)
	f = func(n *html.Node) {
		if n.Type == html.TextNode {
			buf.WriteString(n.Data)
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			f(c)
		}
	}
	f(n)
	return buf.String()
}

func parseNumber(s string) float64 {
	s = strings.ReplaceAll(s, ",", "")
	s = strings.ReplaceAll(s, "$", "")
	s = strings.ReplaceAll(s, "%", "")
	s = strings.TrimSpace(s)
	if s == "" || s == "-" || s == "—" || s == "N/A" {
		return 0
	}
	var v float64
	fmt.Sscanf(s, "%f", &v)
	return v
}

func sanitizeMetricName(name string) string {
	name = strings.ToLower(name)
	name = strings.ReplaceAll(name, " ", "_")
	name = strings.ReplaceAll(name, "/", "_")
	name = strings.ReplaceAll(name, "(", "")
	name = strings.ReplaceAll(name, ")", "")
	name = strings.ReplaceAll(name, ".", "")
	name = strings.ReplaceAll(name, "&", "and")
	name = strings.ReplaceAll(name, "-", "_")
	name = strings.ReplaceAll(name, "__", "_")
	return strings.Trim(name, "_")
}
