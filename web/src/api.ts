const BASE = '/api';

export interface JobStatus {
  state: string;
  exchange: string;
  done: number;
  total: number;
  remaining: number;
  errors: string[];
  started: string;
}

export interface HealthStatus {
  redis: boolean;
  status: string;
}

export interface StockItem {
  code: string;
  name: string;
}

export interface StockListResponse {
  exchange: string;
  stocks: StockItem[];
}

export async function fetchHealth(): Promise<HealthStatus> {
  const res = await fetch(`${BASE}/health`);
  return res.json();
}

export async function startJob(exchange: string, opts?: { limit?: number; symbol?: string; workers?: number; rateLimit?: number; cookie?: string; bypassCache?: boolean }): Promise<JobStatus> {
  const res = await fetch(`${BASE}/jobs`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ exchange, ...opts }),
  });
  return res.json();
}

export async function stopJob(): Promise<void> {
  await fetch(`${BASE}/jobs`, { method: 'DELETE' });
}

export async function fetchJobStatus(): Promise<JobStatus> {
  const res = await fetch(`${BASE}/jobs/status`);
  return res.json();
}

export async function fetchStocks(exchange: string): Promise<StockListResponse> {
  const res = await fetch(`${BASE}/stocks?exchange=${exchange}`);
  return res.json();
}

export interface DownloadedStock {
  code: string;
  exchange: string;
  files: string[];
}

export async function fetchDownloaded(): Promise<{ stocks: DownloadedStock[] }> {
  const res = await fetch(`${BASE}/output/list`);
  return res.json();
}

export interface StockFile { name: string; data: any }
export interface StockDetail { code: string; files: StockFile[] }

export async function fetchStockDetail(exchange: string, code: string): Promise<StockDetail> {
  const res = await fetch(`${BASE}/output/stock/${exchange}/${code}`);
  return res.json();
}

// --- MCP Test API ---

export interface McpMetricsResult {
  code: string;
  exchange: string;
  metrics: Record<string, string[]>;
}

export interface McpPeriodResult {
  code: string;
  exchange: string;
  earliest_date: string;
  latest_date: string;
  statement_types_available: string[];
}

export interface McpFinancialsResult {
  code: string;
  exchange: string;
  period: string;
  data: Record<string, Record<string, Record<string, any>>>;
  metric_sources: Record<string, string>;
  errors?: string[];
}

export async function fetchMcpListMetrics(exchange: string, code: string): Promise<McpMetricsResult> {
  const res = await fetch(`${BASE}/mcp/list-metrics?exchange=${exchange}&code=${code}`);
  if (!res.ok) { const err = await res.json(); throw new Error(err.error || res.statusText); }
  return res.json();
}

export async function fetchMcpDataPeriod(exchange: string, code: string): Promise<McpPeriodResult> {
  const res = await fetch(`${BASE}/mcp/data-period?exchange=${exchange}&code=${code}`);
  if (!res.ok) { const err = await res.json(); throw new Error(err.error || res.statusText); }
  return res.json();
}

export async function fetchMcpFinancials(exchange: string, code: string, metrics: string[], period: string): Promise<McpFinancialsResult> {
  const res = await fetch(`${BASE}/mcp/financials?exchange=${exchange}&code=${code}&metrics=${metrics.join(',')}&period=${period}`);
  if (!res.ok) { const err = await res.json(); throw new Error(err.error || res.statusText); }
  return res.json();
}
