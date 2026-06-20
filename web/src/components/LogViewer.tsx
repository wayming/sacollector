import { useEffect, useRef, useState } from 'react';

const MAX_LINES = 500;

interface Props {
  open: boolean;
  onToggle: () => void;
  embedded?: boolean;
}

export default function LogViewer({ open, onToggle, embedded }: Props) {
  const [lines, setLines] = useState<string[]>([]);
  const bottomRef = useRef<HTMLDivElement>(null);

  useEffect(() => {
    if (!open) { setLines([]); return; }

    const es = new EventSource('/api/logs');
    es.onmessage = (e) => {
      setLines(prev => {
        const next = [...prev, (e.data as string).trimEnd()];
        return next.length > MAX_LINES ? next.slice(next.length - MAX_LINES) : next;
      });
    };
    es.onerror = () => es.close();
    return () => es.close();
  }, [open]);

  useEffect(() => {
    bottomRef.current?.scrollIntoView({ behavior: 'smooth' });
  }, [lines]);

  const pre = (
    <pre style={{ margin: 0, height: '100%', overflow: 'auto' }}>
      {lines.length === 0 && <span style={{ color: '#888' }}>Waiting for output...</span>}
      {lines.map((l, i) => <div key={i}>{l}</div>)}
      <div ref={bottomRef} />
    </pre>
  );

  if (embedded) return open ? pre : null;

  return (
    <div className="card log-panel">
      <h3 style={{ cursor: 'pointer' }} onClick={onToggle}>
        {open ? '▾' : '▸'} Console Output {open && `(${lines.length} lines)`}
      </h3>
      {open && pre}
    </div>
  );
}
