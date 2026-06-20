import { useState, useEffect, useCallback } from 'react';
import { fetchHealth, fetchStocks, startJob, stopJob, fetchJobStatus, fetchDownloaded, fetchStockDetail } from './api';
import type { JobStatus, StockItem, DownloadedStock, StockDetail } from './api';
import LogViewer from './components/LogViewer';

const EXCHANGES = ['HKG', 'ASX', 'SHA', 'SHE', 'NASDAQ'];

function badgeClass(state: string) {
  if (state === 'running') return 'badge badge-running';
  if (state === 'stopping') return 'badge badge-stopping';
  if (state === 'done') return 'badge badge-done';
  return 'badge badge-idle';
}

export default function App() {
  const [redisOk, setRedisOk] = useState(false);
  const [job, setJob] = useState<JobStatus | null>(null);
  const [exchange, setExchange] = useState('HKG');
  const [mode, setMode] = useState<'limit' | 'symbol'>('limit');
  const [limit, setLimit] = useState(10);
  const [workers, setWorkers] = useState(4);
  const [rate, setRate] = useState(1000);
  const [cookie, setCookie] = useState("");
  const [bypassCache, setBypassCache] = useState(false);
  const [stocks, setStocks] = useState<StockItem[]>([]);
  const [symbol, setSymbol] = useState('');
  const [search, setSearch] = useState('');
  const [logOpen, setLogOpen] = useState(false);
  const [downloaded, setDownloaded] = useState<DownloadedStock[]>([]);
  const [expandedEx, setExpandedEx] = useState<Set<string>>(new Set());
  const [dlSearch, setDlSearch] = useState("");
  const [selectedCode, setSelectedCode] = useState<string | null>(null);
  const [detail, setDetail] = useState<StockDetail | null>(null);
  const [detailFile, setDetailFile] = useState(0);

  const refresh = () => fetchDownloaded().then(r => setDownloaded(r.stocks || []));

  useEffect(() => {
    fetchHealth().then(h => setRedisOk(h.redis));
    fetchJobStatus().then(s => { if (s.state !== 'idle') setJob(s); });
    refresh();
  }, []);

  useEffect(() => {
    if (mode !== 'symbol') return;
    setStocks([]);
    fetchStocks(exchange).then(r => {
      if (r.stocks) { setStocks(r.stocks); setSymbol(r.stocks[0]?.code || ''); setSearch(''); }
    });
  }, [exchange, mode]);

  const isRunning = job?.state === 'running' || job?.state === 'stopping';

  useEffect(() => {
    if (!isRunning) return;
    const t = setInterval(async () => {
      const s = await fetchJobStatus();
      setJob(s);
      if (s.state === 'done') { setLogOpen(true); refresh(); }
    }, 2000);
    return () => clearInterval(t);
  }, [isRunning, job?.state]);

  const handleStart = useCallback(async () => {
    const s = mode === 'symbol'
      ? await startJob(exchange, { symbol, workers, rateLimit: rate, cookie, bypassCache })
      : await startJob(exchange, { limit, workers, rateLimit: rate, cookie, bypassCache });
    setJob(s); setLogOpen(true);
  }, [exchange, mode, limit, symbol, workers, rate, cookie, bypassCache]);

  useEffect(() => { setSymbol(filtered[0]?.code || ''); }, [search]);
  const handleStop = useCallback(async () => await stopJob(), []);

  const handleSelect = async (ex: string, code: string) => {
    setSelectedCode(code);
    const d = await fetchStockDetail(ex, code);
    setDetail(d); setDetailFile(0);
  };

  const toggleEx = (ex: string) => {
    setExpandedEx(prev => {
      const next = new Set(prev);
      next.has(ex) ? next.delete(ex) : next.add(ex);
      return next;
    });
  };

  const pct = (job && job.total > 0) ? Math.round((job.done / job.total) * 100) : 0;
  const filtered = stocks.filter(s =>
    !search || s.code.includes(search) || s.name.toLowerCase().includes(search.toLowerCase())
  ).slice(0, 200);

  const fileData = detail?.files?.[detailFile];
  const dates = fileData?.data?.data ? Object.keys(fileData.data.data).sort().reverse() : [];
  const metrics = dates.length > 0 ? Object.keys(fileData!.data.data[dates[0]]) : [];

  // Build tree: exchange → stocks
  const tree: Record<string, DownloadedStock[]> = {};
  downloaded.forEach(d => {
    const ex = d.exchange || '?';
    if (!tree[ex]) tree[ex] = [];
    tree[ex].push(d);
  });
  const sortedExchanges = Object.keys(tree).sort();

  return (
    <>
      <header>
        <h1>sacollector</h1>
        <div style={{ display: 'flex', gap: 12, alignItems: 'center' }}>
        {!logOpen && <button onClick={() => setLogOpen(true)} style={{
          background: 'rgba(255,255,255,.15)', color: '#fff', border: 'none',
          borderRadius: 4, padding: '4px 10px', cursor: 'pointer', fontSize: 12
        }}>Console</button>}
        <span style={{ fontSize: 13, display: 'flex', alignItems: 'center' }}>
          <span className={`dot ${redisOk ? 'ok' : 'err'}`} />
          Redis {redisOk ? 'Connected' : 'Offline'}
        </span>
        </div>
      </header>

      <div className="layout" style={{ gridTemplateColumns: logOpen ? "280px 1fr 1fr" : "280px 1fr" }}>
        {/* Column 1: Job */}
        <div className="col">
          <div className="card" style={{ marginBottom: 16 }}>
            <h3>New Job</h3>
            <div className="radio-group">
              <label><input type="radio" checked={mode === 'limit'} onChange={() => setMode('limit')} /> Top N</label>
              <label><input type="radio" checked={mode === 'symbol'} onChange={() => setMode('symbol')} /> Pick</label>
            </div>
            <div className="form-group">
              <label>Exchange</label>
              <select value={exchange} onChange={e => setExchange(e.target.value)} style={{ width: '100%' }}>
                {EXCHANGES.map(ex => <option key={ex} value={ex}>{ex}</option>)}
              </select>
            </div>
            {mode === 'limit' ? (<>
              <div className="form-group"><label>Limit</label><input type="number" value={limit} onChange={e => setLimit(Number(e.target.value))} min={1} style={{ width: '100%' }} /></div>
            </>) : (<>
              <div className="form-group"><label>Search</label><input type="text" value={search} onChange={e => setSearch(e.target.value)} placeholder="Code or name..." style={{ width: '100%' }} /></div>
              <div className="form-group"><label>Stock ({filtered.length})</label><select value={symbol} onChange={e => setSymbol(e.target.value)} style={{ width: '100%' }} size={6}>{filtered.map(s => <option key={s.code} value={s.code}>{s.code} — {s.name}</option>)}</select></div>
            </>)}
            <div className="form-group"><label>Workers</label><select value={workers} onChange={e => setWorkers(Number(e.target.value))} style={{ width: '100%' }}>{[1,2,4,8,16].map(n => <option key={n} value={n}>{n}</option>)}</select></div>
            <div className="form-group"><label>Rate limit</label><select value={rate} onChange={e => setRate(Number(e.target.value))} style={{ width: '100%' }}>{[0,50,100,250,500,750,1000,2000,3000,5000].map(n => <option key={n} value={n}>{n}ms</option>)}</select></div>
            <div className="form-group"><label>Cookie</label><input type="text" value={cookie} onChange={e => setCookie(e.target.value)} placeholder="session cookie..." style={{ width: '100%', fontSize: 11 }} /></div>
            <label style={{ fontSize: 13, display: 'flex', gap: 6, marginBottom: 12 }}><input type="checkbox" checked={bypassCache} onChange={e => setBypassCache(e.target.checked)} /> Bypass cache</label>
            <div style={{ display: 'flex', gap: 8 }}>
              <button className="btn btn-primary" onClick={handleStart} disabled={isRunning}>Start</button>
              <button className="btn btn-danger" onClick={handleStop} disabled={!isRunning}>Stop</button>
            </div>
          </div>

          {job && (
            <div className="card" style={{ flex: 1, overflow: 'auto' }}>
              <h3>Status</h3>
              <div style={{ display: 'flex', justifyContent: 'space-between', marginBottom: 8 }}>
                <span style={{ fontWeight: 600 }}>{job.exchange}</span>
                <span className={badgeClass(job.state)}>{job.state}</span>
              </div>
              <div className="progress-bar"><div className="progress-fill" style={{ width: `${pct}%` }} /></div>
              <div className="stats">
                <div className="stat"><div className="val ok">{job.done}</div><div className="lbl">Done</div></div>
                <div className="stat"><div className="val">{job.total}</div><div className="lbl">Total</div></div>
                <div className="stat"><div className="val">{job.remaining}</div><div className="lbl">Left</div></div>
                <div className="stat"><div className="val err">{job.errors?.length || 0}</div><div className="lbl">Errors</div></div>
              </div>
              {job.started && <div style={{ fontSize: 12, color: 'var(--text-muted)', marginBottom: 8 }}>{new Date(job.started).toLocaleString()}</div>}
              {job.errors && job.errors.length > 0 && (
                <table><thead><tr><th>Code</th><th>Reason</th></tr></thead>
                  <tbody>{job.errors.map(e => { const [code, ...reason] = e.split(': '); return <tr key={code}><td style={{ fontFamily: 'monospace' }}>{code}</td><td style={{ color: 'var(--text-muted)', fontSize: 12 }}>{reason.join(': ')}</td></tr>; })}</tbody></table>
              )}
            </div>
          )}
        </div>

        {logOpen && (
        <div className="col">
          <div className="card" style={{ display: 'flex', flexDirection: 'column', height: '100%', overflow: 'hidden' }}>
            <h3 style={{ cursor: 'pointer' }} onClick={() => setLogOpen(false)}>▾ Console</h3>
            <div style={{ flex: 1, overflow: 'auto' }}><LogViewer open={true} onToggle={() => setLogOpen(false)} embedded /></div>
          </div>
        </div>
        )}

        {/* Column 3: Results */}
        <div className="col">
          {!selectedCode && (
            <div className="card" style={{ display: 'flex', flexDirection: 'column', height: '100%', overflow: 'hidden' }}>
              <h3>Downloaded ({downloaded.length})</h3>
              <div className="form-group"><input type="text" value={dlSearch} onChange={e => setDlSearch(e.target.value)} placeholder="Filter by code..." style={{ width: '100%' }} /></div>
              {downloaded.length === 0 ? (
                <div className="empty"><p style={{ fontSize: 14 }}>No data yet</p></div>
              ) : (
                <div className="col-scroll" style={{ flex: 1 }}>
                  {sortedExchanges.map(ex => (
                    <div key={ex} style={{ marginBottom: 4 }}>
                      <div
                        onClick={() => toggleEx(ex)}
                        style={{ padding: '6px 10px', cursor: 'pointer', display: 'flex', justifyContent: 'space-between',
                          background: '#f8f9fa', borderRadius: 4, fontWeight: 600, fontSize: 13,
                          border: '1px solid var(--border)' }}>
                        <span>{expandedEx.has(ex) ? '▾' : '▸'} {ex}</span>
                        <span style={{ color: 'var(--text-muted)', fontWeight: 400 }}>{tree[ex].length} stocks</span>
                      </div>
                      {expandedEx.has(ex) && (
                        <div style={{ paddingLeft: 16 }}>
                          {tree[ex].filter(d => !dlSearch || d.code.includes(dlSearch)).slice(0, 200).map(d => (
                            <div key={d.code}
                              onClick={() => handleSelect(d.exchange, d.code)}
                              style={{ padding: '4px 10px', cursor: 'pointer', display: 'flex', justifyContent: 'space-between',
                                fontSize: 12, borderBottom: '1px solid #f3f4f6', borderRadius: 2 }}>
                              <span style={{ fontFamily: 'monospace', fontWeight: 500 }}>{d.code}</span>
                              <span style={{ color: 'var(--text-muted)' }}>{d.files.length}f</span>
                            </div>
                          ))}
                        </div>
                      )}
                    </div>
                  ))}
                </div>
              )}
            </div>
          )}

          {selectedCode && detail && (
            <div className="card" style={{ display: 'flex', flexDirection: 'column', height: '100%', overflow: 'hidden' }}>
              <div style={{ display: 'flex', justifyContent: 'space-between', marginBottom: 12 }}>
                <h3 style={{ margin: 0, fontFamily: 'monospace' }}>{selectedCode}</h3>
                <button className="btn" style={{ padding: '4px 10px', fontSize: 12 }}
                  onClick={() => { setSelectedCode(null); setDetail(null); }}>← Back</button>
              </div>
              <div style={{ display: 'flex', gap: 4, marginBottom: 12, flexWrap: 'wrap' }}>
                {detail.files.map((f, i) => (
                  <button key={f.name} onClick={() => setDetailFile(i)}
                    style={{ padding: '4px 10px', fontSize: 11, cursor: 'pointer',
                      background: i === detailFile ? 'var(--accent)' : '#f3f4f6',
                      color: i === detailFile ? '#fff' : '#555', border: 'none', borderRadius: 4 }}>
                    {f.name.replace('.json', '').replace(`${selectedCode}_`, '').replace(/_/g, ' ')}
                  </button>
                ))}
              </div>
              {fileData && dates.length > 0 && (
                <div className="col-scroll" style={{ flex: 1 }}>
                  <table style={{ fontSize: 12 }}>
                    <thead><tr>
                      <th style={{ position: 'sticky', top: 0, background: '#fff' }}>Metric</th>
                      {dates.map(d => <th key={d} style={{ position: 'sticky', top: 0, background: '#fff', textAlign: 'right', whiteSpace: 'nowrap' }}>{d}</th>)}
                    </tr></thead>
                    <tbody>
                      {metrics.slice(0, 80).map(m => (
                        <tr key={m}>
                          <td style={{ whiteSpace: 'nowrap', maxWidth: 180, overflow: 'hidden', textOverflow: 'ellipsis' }} title={m}>{m.split('.').pop()}</td>
                          {dates.map(d => {
                            const v = fileData.data.data[d]?.[m];
                            const display = v === null || v === undefined ? '-' : typeof v === 'number' ? (Math.abs(v) > 1e9 ? (v / 1e9).toFixed(2) + 'B' : Math.abs(v) > 1e6 ? (v / 1e6).toFixed(1) + 'M' : v.toLocaleString()) : String(v);
                            return <td key={d} style={{ textAlign: 'right', whiteSpace: 'nowrap' }}>{display}</td>;
                          })}
                        </tr>
                      ))}
                    </tbody>
                  </table>
                </div>
              )}
            </div>
          )}
        </div>
      </div>
      
    </>
  );
}
