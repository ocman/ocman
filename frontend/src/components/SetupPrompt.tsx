import { useState } from 'react';
import { useQuery } from '@tanstack/react-query';
import { api } from '../lib/api';
import type { DoctorCheck } from '../lib/api.types';
import './SetupPrompt.css';

const DISMISS_STORAGE_KEY = 'ocman.setup-banner-dismissed';

// What each optional check unlocks. Keep in sync with the Requirements
// section of docs/introduction/_index.md.
const UNLOCKS: Record<string, string> = {
  tmux: 'launching managed OpenCode sessions, /wt worktree sessions and browser terminals',
  lsof: 'discovering OpenCode instances you started yourself',
  whisper: 'voice input in the composer',
  'mcp-listener': 'the dedicated MCP endpoint for OpenCode',
};

function loadDismissed(): string | null {
  try {
    return window.localStorage.getItem(DISMISS_STORAGE_KEY);
  } catch {
    return null;
  }
}

function saveDismissed(key: string) {
  try {
    window.localStorage.setItem(DISMISS_STORAGE_KEY, key);
  } catch {
    // best-effort
  }
}

function CheckList({ checks }: { checks: DoctorCheck[] }) {
  return (
    <ul className="oc-setup-list">
      {checks.map((c) => (
        <li key={c.id}>
          <strong>{c.label}</strong>
          {UNLOCKS[c.id] && <> — unlocks {UNLOCKS[c.id]}</>}
          {c.detail && <div className="oc-setup-detail">{c.detail}</div>}
          {c.hint && <div className="oc-setup-hint">{c.hint}</div>}
        </li>
      ))}
    </ul>
  );
}

/**
 * SetupPrompt explains missing prerequisites from /api/doctor: a blocking
 * panel when a required check fails, a dismissable banner when only
 * optional ones do, nothing otherwise.
 */
export function SetupPrompt() {
  const q = useQuery({
    queryKey: ['doctor'],
    queryFn: ({ signal }) => api.getDoctor(signal),
    refetchOnWindowFocus: true,
    retry: false,
  });
  const [dismissed, setDismissed] = useState<string | null>(loadDismissed);

  const failing = (q.data?.checks ?? []).filter((c) => !c.ok);
  if (failing.length === 0) return null;
  const recheck = (
    <button type="button" data-testid="setup-recheck" disabled={q.isFetching} onClick={() => void q.refetch()}>
      {q.isFetching ? 'Checking…' : 'Re-check'}
    </button>
  );

  const required = failing.filter((c) => c.required);
  if (required.length > 0) {
    return (
      <div className="oc-setup-panel" data-testid="setup-panel" role="alertdialog" aria-label="Setup required">
        <div className="oc-error-boundary">
          <h2>ocman needs a few things before it can start</h2>
          <CheckList checks={required} />
          {q.data?.logPath && <p>Log: <code>{q.data.logPath}</code></p>}
          {recheck}
        </div>
      </div>
    );
  }

  const key = failing.map((c) => c.id).sort().join(',');
  if (dismissed === key) return null;
  const optional = failing.filter((c) => c.id !== 'login-shell-path');
  const pathFailed = optional.length !== failing.length;
  return (
    <div className="oc-error-banner oc-setup-banner" data-testid="setup-banner" role="status">
      <div>
        {optional.length > 0 && <>Some optional tools are missing:<CheckList checks={optional} /></>}
        {pathFailed && (
          <div>Launched from Finder? Apps started that way get a minimal PATH, so tools in your shell may not be found.</div>
        )}
      </div>
      {recheck}
      <button type="button" aria-label="Dismiss" onClick={() => { saveDismissed(key); setDismissed(key); }}>×</button>
    </div>
  );
}
