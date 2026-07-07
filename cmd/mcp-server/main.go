// Command mcp-server is a Model Context Protocol server that exposes
// stock financial data from the sacollector output directory.
//
// It supports two transports:
//   - stdio (default): JSON-RPC 2.0 over stdin/stdout
//   - http: Streamable HTTP transport, specify with -http flag
//
// Provides three tools: list_metrics, get_data_period, and get_financials.
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"

	"sacollector/internal/mcp"

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"
)

func main() {
	outputDir := flag.String("output", "./output", "Path to the sacollector output directory")
	httpMode := flag.Bool("http", false, "Run as HTTP server instead of stdio")
	httpAddr := flag.String("addr", ":8081", "HTTP listen address (only with -http)")
	flag.Parse()

	// Allow override via environment variable
	if envDir := os.Getenv("OUTPUT_DIR"); envDir != "" && *outputDir == "./output" {
		outputDir = &envDir
	}

	// All logging goes to stderr
	log.SetOutput(os.Stderr)
	log.SetPrefix("[mcp-server] ")

	reader := mcp.NewReader(*outputDir)
	server := mcp.NewServer(reader)

	if *httpMode {
		log.Printf("Starting sacollector MCP server (HTTP mode), output dir: %s", *outputDir)
		handler := sdkmcp.NewStreamableHTTPHandler(func(r *http.Request) *sdkmcp.Server {
			return server
		}, nil)

		mux := http.NewServeMux()
		mux.Handle("/mcp", handler)
		mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprintf(w, `{"status":"ok","exchanges":%v}`, len(reader.AvailableExchanges()))
		})
		mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != "/" {
				http.NotFound(w, r)
				return
			}
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			fmt.Fprint(w, indexPage)
		})

		log.Printf("Listening on %s", *httpAddr)
		log.Printf("  GET  /        — this page")
		log.Printf("  GET  /health  — health check")
		log.Printf("  POST /mcp     — MCP JSON-RPC endpoint (for MCP clients)")
		if err := http.ListenAndServe(*httpAddr, mux); err != nil {
			log.Fatalf("HTTP server error: %v", err)
		}
	} else {
		log.Printf("Starting sacollector MCP server (stdio mode), output dir: %s", *outputDir)
		log.Printf("Running on stdio transport...")
		if err := server.Run(context.Background(), &sdkmcp.StdioTransport{}); err != nil {
			log.Fatalf("Server error: %v", err)
		}
	}
}

const indexPage = `<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="UTF-8">
<meta name="viewport" content="width=device-width, initial-scale=1.0">
<title>sacollector MCP Server</title>
<style>
  :root { --bg: #f0f2f5; --card: #fff; --accent: #4f46e5; --text: #333; --muted: #888; --border: #e5e7eb; }
  * { box-sizing: border-box; margin: 0; padding: 0; }
  body { font-family: -apple-system, BlinkMacSystemFont, 'Segoe UI', Roboto, sans-serif; background: var(--bg); color: var(--text); min-height: 100vh; display: flex; justify-content: center; padding: 40px 16px; }
  .container { max-width: 720px; width: 100%; }
  h1 { font-size: 24px; margin-bottom: 8px; }
  .subtitle { color: var(--muted); margin-bottom: 24px; }
  .card { background: var(--card); border-radius: 8px; padding: 20px; box-shadow: 0 1px 3px rgba(0,0,0,.08); border: 1px solid var(--border); margin-bottom: 16px; }
  .card h3 { font-size: 14px; text-transform: uppercase; letter-spacing: .5px; color: var(--muted); margin-bottom: 12px; }
  code { background: #f3f4f6; padding: 2px 6px; border-radius: 3px; font-size: 13px; }
  pre { background: #1e1e1e; color: #d4d4d4; padding: 14px; border-radius: 6px; overflow-x: auto; font-size: 12px; line-height: 1.6; }
  .method { display: inline-block; padding: 2px 8px; border-radius: 3px; font-size: 11px; font-weight: 700; margin-right: 8px; width: 48px; text-align: center; }
  .get { background: #dcfce7; color: #15803d; }
  .post { background: #dbeafe; color: #1d4ed8; }
  .endpoint { display: flex; align-items: center; padding: 8px 0; border-bottom: 1px solid var(--border); }
  .endpoint:last-child { border-bottom: none; }
  .path { font-family: monospace; font-size: 14px; }
  .desc { color: var(--muted); font-size: 13px; margin-left: auto; }
  table { width: 100%; border-collapse: collapse; font-size: 13px; }
  th { text-align: left; padding: 8px 12px; border-bottom: 2px solid var(--border); font-size: 12px; color: var(--muted); text-transform: uppercase; }
  td { padding: 8px 12px; border-bottom: 1px solid #f3f4f6; }
  tr:hover td { background: #f9fafb; }
</style>
</head>
<body>
<div class="container">
  <h1>sacollector MCP Server</h1>
  <p class="subtitle">Model Context Protocol server for stock financial data</p>

  <div class="card">
    <h3>Endpoints</h3>
    <div class="endpoint">
      <span class="method get">GET</span>
      <span class="path">/</span>
      <span class="desc">This page</span>
    </div>
    <div class="endpoint">
      <span class="method get">GET</span>
      <span class="path">/health</span>
      <span class="desc">Health check</span>
    </div>
    <div class="endpoint">
      <span class="method post">POST</span>
      <span class="path">/mcp</span>
      <span class="desc">MCP JSON-RPC (for MCP clients)</span>
    </div>
  </div>

  <div class="card">
    <h3>MCP Tools</h3>
    <table>
      <thead><tr><th>Tool</th><th>Description</th></tr></thead>
      <tbody>
        <tr><td><code>list_metrics</code></td><td>List all financial metrics for a stock, grouped by statement type</td></tr>
        <tr><td><code>get_data_period</code></td><td>Get the earliest and latest date of available data</td></tr>
        <tr><td><code>get_financials</code></td><td>Query financial data for specified metrics and period (1y/2y/5y/all)</td></tr>
      </tbody>
    </table>
  </div>

  <div class="card">
    <h3>Quick Test</h3>
    <p style="margin-bottom:12px;font-size:14px;">Use <code>curl</code> to test the MCP endpoint:</p>
    <pre>curl -X POST http://localhost:8081/mcp \
  -H "Content-Type: application/json" \
  -d '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"test","version":"1.0"}}}'</pre>
  </div>

  <div class="card">
    <h3>HTTP API (Collector Service)</h3>
    <p style="margin-bottom:12px;font-size:14px;">For simple HTTP access, use the collector service on port 8080:</p>
    <table>
      <thead><tr><th>Endpoint</th><th>Method</th><th>Params</th></tr></thead>
      <tbody>
        <tr><td><code>/api/mcp/list-metrics</code></td><td>GET</td><td><code>?exchange=X&code=Y</code></td></tr>
        <tr><td><code>/api/mcp/data-period</code></td><td>GET</td><td><code>?exchange=X&code=Y</code></td></tr>
        <tr><td><code>/api/mcp/financials</code></td><td>GET</td><td><code>?exchange=X&code=Y&metrics=a,b&period=2y</code></td></tr>
      </tbody>
    </table>
  </div>
</div>
</body>
</html>
`
