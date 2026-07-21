/**
 * Presentational section bodies for SessionInfoSidebar, extracted so the
 * sidebar component stays within the size budget. Each takes
 * already-derived data as props; all derivation stays in the parent so
 * this file introduces no behaviour of its own.
 */
import { Link } from 'react-router-dom';
import { formatDuration, formatNumber } from '../lib/format';
import { InfoSidebarSkeleton } from './Skeleton';
import type { Session, SessionInfo } from '../lib/api';

// Human-friendly relabel for known statuses. Unknown values forward
// verbatim so a new platform-side status string still renders without a
// frontend change.
const STATUS_LABEL: Record<string, string> = {
  connected: 'Connected',
  needs_auth: 'Needs auth',
  failed: 'Failed',
};

function statusLabel(status: string): string {
  return STATUS_LABEL[status] ?? status;
}

export function SessionSection({
  session,
  branch,
  branchDirty,
  hasMsgBreakdown,
  msgUser,
  msgAssistant,
  displayCost,
  displayEst,
}: {
  session: Session;
  branch: string | undefined;
  branchDirty: boolean;
  hasMsgBreakdown: boolean;
  msgUser: number;
  msgAssistant: number;
  displayCost: number;
  displayEst: number;
}) {
  return (
    <section className="oc-info-section">
      <header className="oc-info-section-header">Session</header>
      <div className="oc-info-context">
        {branch && (
          <div className="oc-info-row">
            <span className="oc-info-row-label">Branch</span>
            <span className="oc-info-row-value oc-info-row-truncate" title={branch}>
              {branch}
              {branchDirty && <span className="oc-info-branch-dirty"> *</span>}
            </span>
          </div>
        )}
        {session.parentId && (
          <div className="oc-info-row">
            <span className="oc-info-row-label">Parent</span>
            <span className="oc-info-row-value oc-info-row-truncate">
              <Link to={`/session/${encodeURIComponent(session.parentId)}`}>
                View parent session
              </Link>
            </span>
          </div>
        )}
        <div className="oc-info-row">
          <span className="oc-info-row-label">Status</span>
          <span className="oc-info-row-value">
            <span className="oc-info-status-text">{session.status}</span>
          </span>
        </div>
        <div className="oc-info-row">
          <span className="oc-info-row-label">Messages</span>
          <span className="oc-info-row-value">
            {hasMsgBreakdown
              ? `${formatNumber(msgUser)} + ${formatNumber(msgAssistant)}`
              : formatNumber(session.messageCount)}
          </span>
        </div>
        <div className="oc-info-row">
          <span className="oc-info-row-label">Duration</span>
          <span className="oc-info-row-value">{formatDuration(session.durationMs)}</span>
        </div>
        {session.activeDurationMs > 0 && session.activeDurationMs < session.durationMs && (
          <div className="oc-info-row">
            <span
              className="oc-info-row-label"
              title="Time the agent was actually working, excluding idle gaps between turns (user think time, permission prompts answered between turns)."
            >
              Active
            </span>
            <span className="oc-info-row-value">{formatDuration(session.activeDurationMs)}</span>
          </div>
        )}
        {(session.summaryFiles || session.summaryAdditions || session.summaryDeletions) ? (
          <div className="oc-info-row">
            <span className="oc-info-row-label">Changes</span>
            <span className="oc-info-row-value">
              {session.summaryFiles ?? 0}{' '}
              {(session.summaryFiles ?? 0) === 1 ? 'file' : 'files'}{' '}
              <span className="oc-changes-add">+{session.summaryAdditions ?? 0}</span>{' '}
              <span className="oc-changes-del">-{session.summaryDeletions ?? 0}</span>
            </span>
          </div>
        ) : null}
        <div className="oc-info-row">
          <span className="oc-info-row-label">Cost</span>
          <span className="oc-info-row-value">${displayCost.toFixed(2)}</span>
        </div>
        {displayEst > 0 && (
          <div className="oc-info-row">
            <span className="oc-info-row-label">Est</span>
            <span className="oc-info-row-value">${displayEst.toFixed(2)}</span>
          </div>
        )}
      </div>
    </section>
  );
}

export function TokensSection({
  input,
  output,
  cacheRead,
  cacheWrite,
}: {
  input: number;
  output: number;
  cacheRead: number;
  cacheWrite: number;
}) {
  return (
    <section className="oc-info-section">
      <header className="oc-info-section-header">Tokens</header>
      <div className="oc-info-context">
        <div className="oc-info-row">
          <span className="oc-info-row-label">Input</span>
          <span className="oc-info-row-value">{formatNumber(input)}</span>
        </div>
        <div className="oc-info-row">
          <span className="oc-info-row-label">Output</span>
          <span className="oc-info-row-value">{formatNumber(output)}</span>
        </div>
        <div className="oc-info-row">
          <span className="oc-info-row-label">Cache read</span>
          <span className="oc-info-row-value">{formatNumber(cacheRead)}</span>
        </div>
        <div className="oc-info-row">
          <span className="oc-info-row-label">Cache write</span>
          <span className="oc-info-row-value">{formatNumber(cacheWrite)}</span>
        </div>
      </div>
    </section>
  );
}

export function LiveSection({
  loading,
  error,
  data,
  ctxTokens,
  pct,
}: {
  loading: boolean;
  error: string | null;
  data: SessionInfo | null;
  ctxTokens: number;
  pct: number | null;
}) {
  const mcpServers = data?.mcpServers ?? [];
  const lspServers = data?.lspServers ?? [];
  return (
    <>
      {loading && !data && (
        <InfoSidebarSkeleton sections={2} rowsPerSection={3} />
      )}
      {error && (
        <section className="oc-info-section">
          <div className="oc-changes-sidebar-error">Failed to load live info: {error}</div>
        </section>
      )}
      {data && data.supported && (
        <>
          <section className="oc-info-section">
            <header className="oc-info-section-header">Context window</header>
            <div className="oc-info-context">
              <div className="oc-info-row">
                <span className="oc-info-row-label">Tokens</span>
                <span className="oc-info-row-value">{formatNumber(ctxTokens)}</span>
              </div>
              {pct !== null && (
                <div className="oc-info-row">
                  <span className="oc-info-row-label">Used</span>
                  <span className="oc-info-row-value">{pct}%</span>
                </div>
              )}
            </div>
          </section>

          <section className="oc-info-section">
            <header className="oc-info-section-header">MCP</header>
            {mcpServers.length === 0 ? (
              <div className="oc-info-empty">No MCP servers configured.</div>
            ) : (
              <ul className="oc-info-list">
                {mcpServers.map((s) => (
                  <li key={s.name} className="oc-info-list-item">
                    <span className="oc-info-list-name">{s.name}</span>
                    <span
                      className="oc-info-status"
                      title={(s.authHint && `Run: ${s.authHint}`) || s.error || statusLabel(s.status)}
                    >
                      {statusLabel(s.status)}
                    </span>
                  </li>
                ))}
              </ul>
            )}
          </section>

          <section className="oc-info-section">
            <header className="oc-info-section-header">LSP</header>
            {lspServers.length === 0 ? (
              <div className="oc-info-empty">LSPs will activate as files are read.</div>
            ) : (
              <ul className="oc-info-list">
                {lspServers.map((s) => (
                  <li key={s.id} className="oc-info-list-item">
                    <span className="oc-info-list-name">{s.name || s.id}</span>
                    <span className="oc-info-status">{statusLabel(s.status)}</span>
                  </li>
                ))}
              </ul>
            )}
          </section>
        </>
      )}
    </>
  );
}
