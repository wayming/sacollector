import { useState } from 'react';
import { fetchMcpListMetrics, fetchMcpDataPeriod, fetchMcpFinancials } from '../api';
import type { McpMetricsResult, McpPeriodResult, McpFinancialsResult } from '../api';

const EXCHANGES = ['ASX', 'HKG', 'NASDAQ', 'NYSE', 'SHA', 'SHE'];

export default function McpTest() {
  const [exchange, setExchange] = useState('HKG');
  const [code, setCode] = useState('0700');
  const [metricsInput, setMetricsInput] = useState('revenue,netinc,epsBasic,pe,pb');
  const [period, setPeriod] = useState('2y');
  const [loading, setLoading] = useState<string | null>(null);
  const [result, setResult] = useState<any>(null);
  const [error, setError] = useState<string | null>(null);

  const runTool = async (tool: string, fn: () => Promise<any>) => {
    setLoading(tool);
    setError(null);
    setResult(null);
    try {
      const data = await fn();
      setResult({ tool, data });
    } catch (e: any) {
      setError(e.message || String(e));
    } finally {
      setLoading(null);
    }
  };

  const handleListMetrics = () => {
    runTool('list_metrics', () => fetchMcpListMetrics(exchange, code));
  };

  const handleDataPeriod = () => {
    runTool('get_data_period', () => fetchMcpDataPeriod(exchange, code));
  };

  const handleFinancials = () => {
    const metrics = metricsInput.split(',').map(s => s.trim()).filter(Boolean);
    runTool('get_financials', () => fetchMcpFinancials(exchange, code, metrics, period));
  };

  const renderListMetrics = (data: McpMetricsResult) => (
    <div>
      <div style={{ marginBottom: 8, fontSize: 13, color: 'var(--text-muted)' }}>
        {data.code} @ {data.exchange}
      </div>
      {Object.entries(data.metrics).map(([stmt, names]) => (
        <div key={stmt} style={{ marginBottom: 12 }}>
          <div style={{ fontWeight: 600, fontSize: 12, marginBottom: 4, color: 'var(--accent)' }}>
            {stmt} ({names.length})
          </div>
          <div style={{ display: 'flex', flexWrap: 'wrap', gap: 4 }}>
            {names.map(name => (
              <span key={name} style={{
                padding: '2px 8px', fontSize: 11, background: '#f0f4ff',
                borderRadius: 3, fontFamily: 'monospace', cursor: 'pointer'
              }}
                title={`Click to add "${name}" to metrics`}
                onClick={() => setMetricsInput(prev => prev ? `${prev},${name}` : name)}
              >{name}</span>
            ))}
          </div>
        </div>
      ))}
    </div>
  );

  const renderDataPeriod = (data: McpPeriodResult) => (
    <div>
      <div style={{ fontSize: 14, fontFamily: 'monospace', marginBottom: 8 }}>{data.code} @ {data.exchange}</div>
      <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: 12, marginBottom: 12 }}>
        <div className="stat">
          <div className="val ok" style={{ fontSize: 16 }}>{data.earliest_date}</div>
          <div className="lbl">Earliest</div>
        </div>
        <div className="stat">
          <div className="val" style={{ fontSize: 16 }}>{data.latest_date}</div>
          <div className="lbl">Latest</div>
        </div>
      </div>
      <div style={{ fontSize: 12, color: 'var(--text-muted)' }}>
        Available: {data.statement_types_available.join(', ')}
      </div>
    </div>
  );

  const renderFinancials = (data: McpFinancialsResult) => {
    const dates = Object.keys(data.data).sort().reverse();
    const allMetrics = Object.keys(data.metric_sources || {});
    if (dates.length === 0) return <div style={{ color: 'var(--text-muted)' }}>No data in period</div>;
    return (
      <div>
        <div style={{ fontSize: 14, fontFamily: 'monospace', marginBottom: 8 }}>
          {data.code} @ {data.exchange} &middot; {data.period} &middot; {dates.length} periods
        </div>
        <div style={{ marginBottom: 8, fontSize: 11, color: 'var(--text-muted)' }}>
          {allMetrics.map(m => (
            <span key={m} style={{ marginRight: 12 }}>
              {m} <span style={{ opacity: 0.5 }}>({data.metric_sources[m]})</span>
            </span>
          ))}
        </div>
        <div style={{ overflowX: 'auto' }}>
          <table style={{ fontSize: 11 }}>
            <thead>
              <tr>
                <th style={{ position: 'sticky', top: 0, background: '#fff' }}>Date</th>
                {allMetrics.map(m => (
                  <th key={m} style={{ position: 'sticky', top: 0, background: '#fff', textAlign: 'right', whiteSpace: 'nowrap' }}>
                    {m}
                  </th>
                ))}
              </tr>
            </thead>
            <tbody>
              {dates.map(date => (
                <tr key={date}>
                  <td style={{ whiteSpace: 'nowrap', fontFamily: 'monospace' }}>{date}</td>
                  {allMetrics.map(metric => {
                    const stmt = data.metric_sources[metric];
                    const val = data.data[date]?.[stmt]?.[metric];
                    if (val === null || val === undefined) return <td key={metric} style={{ textAlign: 'right', color: '#ccc' }}>-</td>;
                    const display = typeof val === 'number'
                      ? (Math.abs(val) > 1e11 ? (val / 1e12).toFixed(2) + 'T'
                        : Math.abs(val) > 1e8 ? (val / 1e9).toFixed(2) + 'B'
                        : Math.abs(val) > 1e5 ? (val / 1e6).toFixed(1) + 'M'
                        : (Math.abs(val) < 1 && val !== 0) ? val.toFixed(4)
                        : val.toLocaleString())
                      : String(val);
                    return <td key={metric} style={{ textAlign: 'right', whiteSpace: 'nowrap' }}>{display}</td>;
                  })}
                </tr>
              ))}
            </tbody>
          </table>
        </div>
        {data.errors && data.errors.length > 0 && (
          <div style={{ marginTop: 8, fontSize: 12, color: 'var(--danger)' }}>
            {data.errors.map((e, i) => <div key={i}>{e}</div>)}
          </div>
        )}
      </div>
    );
  };

  return (
    <div className="card" style={{ display: 'flex', flexDirection: 'column', height: '100%', overflow: 'hidden' }}>
      <h3>MCP Test</h3>
      <p style={{ fontSize: 12, color: 'var(--text-muted)', marginBottom: 16 }}>
        Test the MCP server tools: list available metrics, check data period, or query financial data.
      </p>

      {/* Controls */}
      <div style={{ display: 'flex', gap: 8, marginBottom: 16, flexWrap: 'wrap', alignItems: 'flex-end' }}>
        <div className="form-group" style={{ marginBottom: 0 }}>
          <label>Exchange</label>
          <select value={exchange} onChange={e => setExchange(e.target.value)}>
            {EXCHANGES.map(ex => <option key={ex} value={ex}>{ex}</option>)}
          </select>
        </div>
        <div className="form-group" style={{ marginBottom: 0 }}>
          <label>Code</label>
          <input type="text" value={code} onChange={e => setCode(e.target.value.toUpperCase())}
            style={{ width: 100, fontFamily: 'monospace' }} />
        </div>
        <div className="form-group" style={{ marginBottom: 0, flex: 1, minWidth: 200 }}>
          <label>Metrics (comma-separated)</label>
          <input type="text" value={metricsInput} onChange={e => setMetricsInput(e.target.value)}
            style={{ width: '100%', fontFamily: 'monospace', fontSize: 11 }} />
        </div>
        <div className="form-group" style={{ marginBottom: 0 }}>
          <label>Period</label>
          <select value={period} onChange={e => setPeriod(e.target.value)}>
            {['1y', '2y', '5y', 'all'].map(p => <option key={p} value={p}>{p}</option>)}
          </select>
        </div>
      </div>

      {/* Action buttons */}
      <div style={{ display: 'flex', gap: 8, marginBottom: 16, flexWrap: 'wrap' }}>
        <button className="btn btn-primary" onClick={handleListMetrics} disabled={loading !== null}>
          {loading === 'list_metrics' ? '...' : '1. List Metrics'}
        </button>
        <button className="btn btn-primary" onClick={handleDataPeriod} disabled={loading !== null}>
          {loading === 'get_data_period' ? '...' : '2. Get Data Period'}
        </button>
        <button className="btn btn-primary" onClick={handleFinancials} disabled={loading !== null}>
          {loading === 'get_financials' ? '...' : '3. Get Financials'}
        </button>
      </div>

      {error && (
        <div style={{ padding: 12, background: '#fff0f0', borderRadius: 4, marginBottom: 16, fontSize: 13, color: 'var(--danger)' }}>
          {error}
        </div>
      )}

      {/* Results */}
      <div style={{ flex: 1, overflow: 'auto' }}>
        {!result && !loading && (
          <div className="empty">
            <p style={{ fontSize: 14 }}>Select a tool and click to see results</p>
            <p style={{ fontSize: 12, color: 'var(--text-muted)' }}>
              Try HKG:0700 (Tencent) or ASX:MGX with metrics like revenue, netinc, pe, pb
            </p>
          </div>
        )}
        {result && result.tool === 'list_metrics' && renderListMetrics(result.data)}
        {result && result.tool === 'get_data_period' && renderDataPeriod(result.data)}
        {result && result.tool === 'get_financials' && renderFinancials(result.data)}
      </div>
    </div>
  );
}
