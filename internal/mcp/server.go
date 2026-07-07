package mcp

import (
	"log"

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"
)

// NewServer creates and configures an MCP server with all stock financial tools registered.
func NewServer(reader *Reader) *sdkmcp.Server {
	server := sdkmcp.NewServer(
		&sdkmcp.Implementation{
			Name:    "sacollector-mcp",
			Version: "1.0.0",
		},
		nil,
	)

	// Tool 1: list_metrics
	sdkmcp.AddTool(server, &sdkmcp.Tool{
		Name:        "list_metrics",
		Description: "List all available financial metrics for a given stock, grouped by statement type (income-statement, balance-sheet, cash-flow-statement, ratios).",
	}, ListMetrics(reader))

	// Tool 2: get_data_period
	sdkmcp.AddTool(server, &sdkmcp.Tool{
		Name:        "get_data_period",
		Description: "Return the earliest and latest date for which financial data exists for a given stock, across all statement types.",
	}, GetDataPeriod(reader))

	// Tool 3: get_financials
	sdkmcp.AddTool(server, &sdkmcp.Tool{
		Name:        "get_financials",
		Description: "Return financial data for specified metrics over a given time period. Period can be '1y', '2y', '5y', or 'all'. Metrics can be prefixed with statement type (e.g., 'income-statement.revenue') for disambiguation, or bare metric names will be resolved automatically.",
	}, GetFinancials(reader))

	log.Printf("[mcp] Server initialized with 3 tools: list_metrics, get_data_period, get_financials")
	return server
}
