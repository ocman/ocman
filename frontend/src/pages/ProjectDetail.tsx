import { useState, useEffect, useCallback } from 'react';
import { useParams } from 'react-router-dom';
import type { Session } from '../lib/api';
import { usePageTitle } from '../lib/headerContext';
import { SessionTable } from '../components/SessionTable';
import { useTmux } from '../lib/useTmux';
import { useApiStore, useApiRequest } from '../lib/apiStore';

export function ProjectDetail() {
  const { '*': directory } = useParams();
  const projectName = directory?.split('/').pop() || 'Project';
  usePageTitle(projectName);
  const tmux = useTmux();
  const [sessions, setSessions] = useState<Session[]>([]);
  const getSessions = useApiStore((state) => state.getSessions);
  const sessionsRequest = useApiRequest(directory ? `sessions:get:dir:${directory}` : 'sessions:get');

  const load = useCallback(async () => {
    if (!directory) {
      setSessions([]);
      return;
    }

    try {
      const nextSessions = await getSessions({ dir: directory });
      setSessions(nextSessions);
    } catch {
      // error tracked by useApiRequest
    }
  }, [directory, getSessions]);

  useEffect(() => {
    let cancelled = false;

    async function loadProject() {
      if (!directory) {
        if (!cancelled) setSessions([]);
        return;
      }

      try {
        const nextSessions = await getSessions({ dir: directory });
        if (cancelled) return;
        setSessions(nextSessions);
      } catch {
        // error tracked by useApiRequest
      }
    }

    void loadProject();
    return () => {
      cancelled = true;
    };
  }, [directory, getSessions]);

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
      <SessionTable sessions={sessions} loading={sessionsRequest.loading && sessions.length === 0} tmux={tmux} includeArchived />
    </div>
  );
}
