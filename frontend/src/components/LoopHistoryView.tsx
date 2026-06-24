import { useEffect, useState } from 'react';
import { Link } from 'react-router-dom';
import './LoopHistoryView.css';
import { api } from '../lib/api';
import type { Loop, LoopChildSession, LoopIteration } from '../lib/api.types';
import { relativeTime } from '../lib/format';
import { formatDurationMs, nextRunLabel } from '../lib/loopFormat';

interface LoopHistoryViewProps {
  loop: Loop;
}

const TERMINAL_SESSION_STATUSES = ['completed', 'error', 'cancelled'];

function sessionStateLabel(status: string): string {
  return TERMINAL_SESSION_STATUSES.includes(status) ? 'done' : 'running';
}

function iterationDuration(it: LoopIteration, child?: LoopChildSession): string {
  if (child) {
    const end = child.completedAt > 0 ? child.completedAt : Date.now();
    return formatDurationMs(end - child.createdAt) + (child.completedAt > 0 ? '' : '…');
  }
  if (it.completedAt > 0 && it.firedAt > 0) {
    return formatDurationMs(it.completedAt - it.firedAt);
  }
  return '';
}

function budgetLine(loop: Loop): string {
  const parts = [`${loop.iteration}/${loop.stopConditions?.max_iterations ?? '?'} iters`];
  if (loop.stopConditions?.max_cost_usd) {
    parts.push(`$${loop.costUSD.toFixed(2)}/$${loop.stopConditions.max_cost_usd.toFixed(2)}`);
  }
  if (loop.errorStreak > 0) parts.push(`${loop.errorStreak} errors`);
  return parts.join(' · ');
}

/**
 * LoopHistoryView renders a loop's detail INLINE inside its sidebar row
 * (expanded under the row): budget/next-run summary, the iteration audit
 * table (session state, duration, link) and any
 * sub-loops. Data is fetched on mount, so the row only pays for it when
 * the user expands history.
 */
export function LoopHistoryView({ loop }: LoopHistoryViewProps) {
  const [iterations, setIterations] = useState<LoopIteration[]>([]);
  const [children, setChildren] = useState<Record<string, LoopChildSession>>({});
  const [subLoops, setSubLoops] = useState<Loop[]>([]);
  const [loading, setLoading] = useState(true);

  useEffect(() => {
    let cancelled = false;
    api.loops
      .get(loop.id)
      .then((detail) => {
        if (cancelled) return;
        setIterations(detail.iterations ?? []);
        const byId: Record<string, LoopChildSession> = {};
        for (const c of detail.children ?? []) byId[c.id] = c;
        setChildren(byId);
        setSubLoops(detail.subLoops ?? []);
        setLoading(false);
      })
      .catch(() => {
        if (!cancelled) setLoading(false);
      });
    return () => {
      cancelled = true;
    };
  }, [loop.id]);

  return (
    <div className="oc-loops-history" data-testid="loop-history">
      <div className="oc-loop-history-meta">
        <span data-testid="loop-budget">{budgetLine(loop)}</span>
        {nextRunLabel(loop) && (
          <span data-testid="loop-next-run">next run: {nextRunLabel(loop)}</span>
        )}
      </div>

      {loading && <div className="oc-loops-history-empty">Loading history…</div>}
      {!loading && iterations.length === 0 && (
        <div className="oc-loops-history-empty">No iterations yet.</div>
      )}
      {!loading && iterations.length > 0 && (
        <table className="oc-loops-history-table">
          <thead>
            <tr>
              <th>#</th>
              <th>Result</th>
              <th>State</th>
              <th>Duration</th>
              <th>When</th>
              <th>Detail</th>
              <th></th>
            </tr>
          </thead>
          <tbody>
            {iterations.map((it) => {
              const sessionId = it.childSessionID || it.targetSessionID;
              const child = sessionId ? children[sessionId] : undefined;
              return (
                <tr key={it.id} data-outcome={it.outcome}>
                  <td className="oc-loops-hcell-seq">{it.seq}</td>
                  <td className="oc-loops-hcell-outcome">{it.outcome}</td>
                  <td>
                    {child ? (
                      <span data-testid="loop-history-session-state" data-status={child.status}>
                        {sessionStateLabel(child.status)}
                      </span>
                    ) : (
                      '—'
                    )}
                  </td>
                  <td data-testid="loop-history-duration">{iterationDuration(it, child) || '—'}</td>
                  <td className="oc-loops-hcell-when">{relativeTime(it.firedAt)}</td>
                  <td className="oc-loops-hcell-detail" title={it.triggerDetail || it.renderedPrompt}>
                    {it.triggerDetail || it.renderedPrompt}
                  </td>
                  <td>
                    {sessionId ? (
                      <Link
                        to={`/session/${encodeURIComponent(sessionId)}`}
                        data-testid="loop-history-session-link"
                      >
                        Show
                      </Link>
                    ) : null}
                  </td>
                </tr>
              );
            })}
          </tbody>
        </table>
      )}
      {subLoops.length > 0 && (
        <div className="oc-loops-subloops" data-testid="loop-subloops">
          <div className="oc-loops-subloops-head">Sub-loops</div>
          {subLoops.map((sub) => (
            <div key={sub.id} className="oc-loops-subloop" data-loop-state={sub.state}>
              <span className="oc-loops-subloop-title">{sub.title || sub.id}</span>
              <span className="oc-loops-subloop-state">{sub.state}</span>
              <span className="oc-loops-subloop-budget">
                {sub.iteration}/{sub.stopConditions?.max_iterations ?? '?'} · ${sub.costUSD.toFixed(2)}
              </span>
            </div>
          ))}
        </div>
      )}
    </div>
  );
}
