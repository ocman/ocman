import { useId, useRef, useState, type CSSProperties } from 'react';
import { useQuery } from '@tanstack/react-query';
import { api } from '../lib/api';
import type { DoctorCheck } from '../lib/api.types';
import { readStorage, writeStorage } from '../lib/storage';
import { Button, ButtonGroup } from './Control';
import { ErrorState } from './ErrorState';
import { Popover } from './Popover';
import styles from './SetupPrompt.module.css';

const DISMISS_STORAGE_KEY = 'ocman.setup-banner-dismissed';
const PATH_CHECK = 'login-shell-path';

// What each optional check unlocks. Keep in sync with the Requirements
// section of docs/introduction/_index.md.
const UNLOCKS: Record<string, string> = {
  tmux: 'launching managed sessions, /wt worktree sessions and browser terminals',
  'mcp-listener': 'the dedicated MCP endpoint',
};

function useDoctor() {
  return useQuery({
    queryKey: ['doctor'],
    queryFn: ({ signal }) => api.getDoctor(signal),
    refetchOnWindowFocus: true,
    retry: false,
  });
}

function CheckList({ checks }: { checks: DoctorCheck[] }) {
  return (
    <ul className={styles.list}>
      {checks.map((c) => (
        <li key={c.id}>
          <strong>{c.label}</strong>
          {UNLOCKS[c.id] && <> — unlocks {UNLOCKS[c.id]}</>}
          {c.detail && <div className={styles.detail}>{c.detail}</div>}
          {c.hint && <div className={styles.hint}>{c.hint}</div>}
        </li>
      ))}
    </ul>
  );
}

function Recheck({ q }: { q: ReturnType<typeof useDoctor> }) {
  return (
    <Button type="button" size="small" data-testid="setup-recheck" aria-busy={q.isFetching} disabled={q.isFetching} onClick={() => void q.refetch()}>
      {q.isFetching ? 'Checking…' : 'Re-check'}
    </Button>
  );
}

/**
 * SetupPrompt blocks the app with a full-screen panel while a required
 * /api/doctor check fails. Optional checks are SetupStatus's job.
 */
export function SetupPrompt() {
  const q = useDoctor();
  const required = (q.data?.checks ?? []).filter((c) => !c.ok && c.required);
  if (required.length === 0) return null;
  return (
    <div className={styles.panel} data-testid="setup-panel" role="alertdialog" aria-label="Setup required">
      <ErrorState title="ocman needs a few things before it can start">
        <CheckList checks={required} />
        {q.data?.logPath && <p className={styles.log}>Log: <code>{q.data.logPath}</code></p>}
        <Recheck q={q} />
      </ErrorState>
    </div>
  );
}

// Matches .oc-popover's width: min(340px, 100vw - 24px).
const POPOVER_WIDTH = 340;
const VIEWPORT_MARGIN = 12;

/**
 * Right-aligns the popover with the chip, but keeps it inside both viewport
 * margins: on phones other header controls can follow the chip.
 */
function popoverLeft(anchorRight: number, viewportWidth: number) {
  const width = Math.min(POPOVER_WIDTH, viewportWidth - 2 * VIEWPORT_MARGIN);
  return Math.max(VIEWPORT_MARGIN, Math.min(anchorRight - width, viewportWidth - VIEWPORT_MARGIN - width));
}

/**
 * SetupStatus is a small warning chip in the app header for failing
 * optional checks. It takes no page space; its popover explains what each
 * check unlocks and offers Re-check and Hide. Hiding lasts until the set of
 * failing checks changes.
 */
export function SetupStatus() {
  const q = useDoctor();
  const id = useId();
  const trigger = useRef<HTMLButtonElement>(null);
  const [position, setPosition] = useState<CSSProperties | null>(null);
  const [dismissed, setDismissed] = useState<string | null>(() => readStorage(DISMISS_STORAGE_KEY));
  const failing = (q.data?.checks ?? []).filter((c) => !c.ok);
  if (failing.length === 0 || failing.some((c) => c.required)) return null;
  const key = failing.map((c) => c.id).sort().join(',');
  if (dismissed === key) return null;

  const open = position !== null;
  const close = () => setPosition(null);
  const toggle = () => {
    if (open) return close();
    const rect = trigger.current?.getBoundingClientRect();
    if (rect) setPosition({ top: rect.bottom + 6, left: popoverLeft(rect.right, window.innerWidth) });
  };
  const optional = failing.filter((c) => c.id !== PATH_CHECK);
  const pathCheck = failing.find((c) => c.id === PATH_CHECK);
  const label = `Optional setup: ${failing.length} ${failing.length === 1 ? 'check needs' : 'checks need'} attention`;
  return <>
    <Button ref={trigger} type="button" variant="ghost" size="compact" className={styles.chip} data-testid="setup-status-trigger"
      aria-label={label} title={label} aria-haspopup="dialog" aria-expanded={open} aria-controls={open ? id : undefined} onClick={toggle}>
      <i className="bi bi-exclamation-triangle" aria-hidden="true" />
      <span className={styles.chipText}>Setup</span>
      <span className={styles.count}>{failing.length}</span>
    </Button>
    <Popover open={open} onClose={close} triggerRef={trigger} id={id} label="Optional setup" style={position ?? undefined}>
      <div className={styles.content} data-testid="setup-banner">
        <h2 className={styles.title}>Optional setup</h2>
        <p className={styles.lead}>ocman works without these; they unlock extra features.</p>
        {optional.length > 0 && <CheckList checks={optional} />}
        {pathCheck && (
          <p className={styles.path}>
            Could not read your login shell PATH, so tools in your shell may not be found. Launched from Finder? Apps started that way get a minimal PATH.
            {pathCheck.detail && <span className={styles.detail}> {pathCheck.detail}</span>}
          </p>
        )}
        <ButtonGroup label="Setup actions" className={styles.actions}>
          <Recheck q={q} />
          <Button type="button" size="small" variant="ghost" onClick={() => { writeStorage(DISMISS_STORAGE_KEY, key); setDismissed(key); close(); }}>
            Hide until something changes
          </Button>
        </ButtonGroup>
      </div>
    </Popover>
  </>;
}
