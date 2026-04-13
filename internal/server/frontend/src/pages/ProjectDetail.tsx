import { useState, useEffect, useCallback } from 'react';
import { useParams } from 'react-router-dom';
import { api } from '../lib/api';
import type { Session } from '../lib/api';
import { SessionTable } from '../components/SessionTable';

export function ProjectDetail() {
  const { '*': directory } = useParams();
  const [sessions, setSessions] = useState<Session[]>([]);
  const [loading, setLoading] = useState(true);

  const load = useCallback(async () => {
    if (directory) setSessions(await api.sessions({ dir: directory }));
    setLoading(false);
  }, [directory]);

  useEffect(() => { load(); }, [load]);

  // Auto-refresh every second
  useEffect(() => {
    const id = setInterval(load, 1000);
    return () => clearInterval(id);
  }, [load]);

  return (
    <div style={{ flex: 1, overflowY: 'auto', minHeight: 0 }}>
      <h2 className="section-title">{directory}</h2>
      <SessionTable sessions={sessions} loading={loading} />
    </div>
  );
}
