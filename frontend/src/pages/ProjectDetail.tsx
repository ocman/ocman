import { useState, useEffect, useCallback } from 'react';
import { useParams } from 'react-router-dom';
import { api } from '../lib/api';
import type { Session } from '../lib/api';
import { usePageTitle } from '../lib/headerContext';
import { SessionTable } from '../components/SessionTable';
import { useTmux } from '../lib/useTmux';

export function ProjectDetail() {
  const { '*': directory } = useParams();
  const projectName = directory?.split('/').pop() || 'Project';
  usePageTitle(projectName);
  const tmux = useTmux();
  const [sessions, setSessions] = useState<Session[]>([]);
  const [loading, setLoading] = useState(true);

  const load = useCallback(async () => {
    if (directory) setSessions(await api.sessions({ dir: directory }));
    setLoading(false);
  }, [directory]);

  useEffect(() => { load(); }, [load]);

  // Auto-refresh every 5 seconds
  useEffect(() => {
    const id = setInterval(load, 5000);
    return () => clearInterval(id);
  }, [load]);

  return (
    <div style={{ flex: 1, overflowY: 'auto', minHeight: 0 }}>
      <h2 className="section-title" style={{ display: 'flex', alignItems: 'center', gap: 12 }}>
        <span style={{ flex: 1, minWidth: 0, overflow: 'hidden', textOverflow: 'ellipsis' }}>{directory}</span>
        {directory && (
          <a href={`vscode://file${directory}`} className="vscode-btn" title="Open in VS Code">VS Code</a>
        )}
      </h2>
      <SessionTable sessions={sessions} loading={loading} tmux={tmux} />
    </div>
  );
}
