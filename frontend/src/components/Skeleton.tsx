import './Skeleton.css';

// ---------------------------------------------------------------------------
// Skeleton — base shimmer block.
//
// Use via the preset wrappers below, or directly:
//   <Skeleton className="oc-skeleton-line" style={{ width: '60%' }} />
// ---------------------------------------------------------------------------

interface SkeletonProps {
  className?: string;
  style?: React.CSSProperties;
  'aria-hidden'?: boolean;
}

export function Skeleton({ className = '', style, 'aria-hidden': ariaHidden = true }: SkeletonProps) {
  return (
    <span
      className={`oc-skeleton ${className}`.trim()}
      style={style}
      aria-hidden={ariaHidden}
    />
  );
}

// ---------------------------------------------------------------------------
// SessionTableSkeleton — fake table rows matching SessionTable's column
// structure (session · project? · activity · started · action).
// ---------------------------------------------------------------------------

interface SessionTableSkeletonProps {
  rows?: number;
  showProject?: boolean;
}

export function SessionTableSkeleton({ rows = 5, showProject = false }: SessionTableSkeletonProps) {
  return (
    <table aria-busy="true" aria-label="Loading sessions">
      <thead>
        <tr>
          <th>Session</th>
          {showProject && <th>Project</th>}
          <th>Activity</th>
          <th>Started</th>
          <th style={{ width: 44 }} />
        </tr>
      </thead>
      <tbody className="oc-skeleton-tbody">
        {Array.from({ length: rows }, (_, i) => (
          <tr key={i}>
            <td>
              <div style={{ display: 'flex', alignItems: 'center', gap: 6, marginBottom: 4 }}>
                <Skeleton className="oc-skeleton-badge" style={{ width: 10, height: 10, borderRadius: '50%' }} />
                <Skeleton className="oc-skeleton-line" style={{ width: `${50 + (i % 3) * 20}%` }} />
              </div>
              <Skeleton className="oc-skeleton-line" style={{ width: '40%', height: 10 }} />
            </td>
            {showProject && (
              <td>
                <Skeleton className="oc-skeleton-line" style={{ width: '60%' }} />
              </td>
            )}
            <td>
              <Skeleton className="oc-skeleton-line" style={{ width: '55%' }} />
            </td>
            <td>
              <Skeleton className="oc-skeleton-line" style={{ width: '45%' }} />
            </td>
            <td />
          </tr>
        ))}
      </tbody>
    </table>
  );
}

// ---------------------------------------------------------------------------
// WorktreesTableSkeleton — fake rows for the WorktreesView table
// (branch · path · sessions · last activity · actions).
// ---------------------------------------------------------------------------

interface WorktreesTableSkeletonProps {
  rows?: number;
}

export function WorktreesTableSkeleton({ rows = 3 }: WorktreesTableSkeletonProps) {
  return (
    <table aria-busy="true" aria-label="Loading worktrees">
      <thead>
        <tr>
          <th>Branch</th>
          <th>Path</th>
          <th>Sessions</th>
          <th>Last activity</th>
          <th>Actions</th>
        </tr>
      </thead>
      <tbody className="oc-skeleton-tbody">
        {Array.from({ length: rows }, (_, i) => (
          <tr key={i}>
            <td>
              <Skeleton className="oc-skeleton-line" style={{ width: `${40 + (i % 2) * 20}%` }} />
            </td>
            <td>
              <Skeleton className="oc-skeleton-line" style={{ width: '55%', marginBottom: 4 }} />
              <Skeleton className="oc-skeleton-line" style={{ width: '80%', height: 10 }} />
            </td>
            <td>
              <Skeleton className="oc-skeleton-line" style={{ width: 24 }} />
            </td>
            <td>
              <Skeleton className="oc-skeleton-line" style={{ width: '50%' }} />
            </td>
            <td>
              <div style={{ display: 'flex', gap: 6 }}>
                <Skeleton className="oc-skeleton-line" style={{ width: 48, height: 24 }} />
                <Skeleton className="oc-skeleton-line" style={{ width: 64, height: 24 }} />
              </div>
            </td>
          </tr>
        ))}
      </tbody>
    </table>
  );
}

// ---------------------------------------------------------------------------
// SidebarFileListSkeleton — placeholder for SessionChangesSidebar and
// WorkingTreeChangesSidebar body while data is absent.
// ---------------------------------------------------------------------------

interface SidebarFileListSkeletonProps {
  rows?: number;
}

export function SidebarFileListSkeleton({ rows = 6 }: SidebarFileListSkeletonProps) {
  return (
    <div className="oc-skeleton-sidebar-rows" aria-busy="true" aria-label="Loading file list">
      {Array.from({ length: rows }, (_, i) => (
        <div key={i} className="oc-skeleton-sidebar-row">
          {/* status badge */}
          <Skeleton className="oc-skeleton-line" style={{ width: 14, height: 14, flexShrink: 0 }} />
          {/* file path */}
          <Skeleton className="oc-skeleton-line" style={{ flex: 1, width: `${40 + (i % 4) * 12}%` }} />
          {/* +/- counts */}
          <Skeleton className="oc-skeleton-line" style={{ width: 36, flexShrink: 0 }} />
        </div>
      ))}
    </div>
  );
}

// ---------------------------------------------------------------------------
// InfoSidebarSkeleton — placeholder for SessionInfoSidebar's live section.
// ---------------------------------------------------------------------------

interface InfoSidebarSkeletonProps {
  sections?: number;
  rowsPerSection?: number;
}

export function InfoSidebarSkeleton({ sections = 2, rowsPerSection = 3 }: InfoSidebarSkeletonProps) {
  return (
    <div aria-busy="true" aria-label="Loading session info">
      {Array.from({ length: sections }, (_, si) => (
        <div key={si} className="oc-skeleton-info-section">
          {/* section header */}
          <Skeleton className="oc-skeleton-line" style={{ width: `${30 + si * 10}%`, height: 10 }} />
          {Array.from({ length: rowsPerSection }, (_, ri) => (
            <div key={ri} className="oc-skeleton-info-row">
              <Skeleton className="oc-skeleton-line" style={{ width: '35%' }} />
              <Skeleton className="oc-skeleton-line" style={{ width: `${25 + ri * 8}%` }} />
            </div>
          ))}
        </div>
      ))}
    </div>
  );
}

// ---------------------------------------------------------------------------
// ThreadSkeleton — placeholder for the conversation thread while a session
// is loading. Renders alternating user / assistant bubble shapes.
// ---------------------------------------------------------------------------

interface ThreadSkeletonProps {
  rows?: number;
}

export function ThreadSkeleton({ rows = 4 }: ThreadSkeletonProps) {
  // Alternate between user (right-aligned, narrower) and assistant
  // (left-aligned, wider with a multi-line body) bubbles.
  const bubbles = Array.from({ length: rows }, (_, i) => ({
    isUser: i % 2 === 0,
    lines: i % 2 === 0 ? 1 : 2 + (i % 3),
  }));
  return (
    <div
      className="oc-skeleton-thread"
      aria-busy="true"
      aria-label="Loading conversation"
      data-testid="thread-skeleton"
    >
      {bubbles.map((b, i) => (
        <div
          key={i}
          className={`oc-skeleton-bubble${b.isUser ? ' oc-skeleton-bubble--user' : ''}`}
        >
          <div className="oc-skeleton-bubble-body">
            {Array.from({ length: b.lines }, (_, li) => (
              <Skeleton
                key={li}
                className="oc-skeleton-line"
                style={{
                  width: b.isUser
                    ? `${50 + (i % 3) * 10}%`
                    : `${65 + (li % 3) * 10}%`,
                  ...(li < b.lines - 1 ? {} : { width: b.isUser ? '40%' : '45%' }),
                }}
              />
            ))}
          </div>
        </div>
      ))}
    </div>
  );
}

// ---------------------------------------------------------------------------
// SessionSidebarListSkeleton — placeholder rows for the left session-list
// sidebar in SessionDetail.
// ---------------------------------------------------------------------------

interface SessionSidebarListSkeletonProps {
  rows?: number;
}

export function SessionSidebarListSkeleton({ rows = 5 }: SessionSidebarListSkeletonProps) {
  return (
    <div
      className="oc-skeleton-sidebar-rows"
      aria-busy="true"
      aria-label="Loading sessions"
      style={{ padding: '8px 0' }}
    >
      {Array.from({ length: rows }, (_, i) => (
        <div
          key={i}
          className="oc-skeleton-sidebar-row"
          style={{ padding: '6px 12px', gap: 6 }}
        >
          {/* status dot */}
          <Skeleton style={{ width: 8, height: 8, borderRadius: '50%', flexShrink: 0 }} />
          <div style={{ flex: 1, display: 'flex', flexDirection: 'column', gap: 4 }}>
            <Skeleton className="oc-skeleton-line" style={{ width: `${45 + (i % 3) * 15}%` }} />
            <Skeleton className="oc-skeleton-line" style={{ width: '35%', height: 10 }} />
          </div>
        </div>
      ))}
    </div>
  );
}

// ---------------------------------------------------------------------------
// TerminalSkeleton — placeholder for the in-app terminal while a tab is
// (re)connecting. Renders a few fake monospace command lines so clicking
// a terminal tab gives instant visual feedback before the PTY attaches.
// ---------------------------------------------------------------------------

interface TerminalSkeletonProps {
  rows?: number;
}

export function TerminalSkeleton({ rows = 6 }: TerminalSkeletonProps) {
  // Pseudo-random but stable line widths so the fake output looks like
  // a real terminal scrollback rather than uniform bars.
  const widths = [38, 64, 22, 52, 30, 70, 18, 46];
  return (
    <div
      className="oc-skeleton-terminal"
      aria-busy="true"
      aria-label="Connecting terminal"
      data-testid="terminal-skeleton"
    >
      {Array.from({ length: rows }, (_, i) => (
        <div key={i} className="oc-skeleton-terminal-row">
          {/* prompt glyph */}
          <Skeleton className="oc-skeleton-line" style={{ width: 10, flexShrink: 0 }} />
          <Skeleton
            className="oc-skeleton-line"
            style={{ width: `${widths[i % widths.length]}%` }}
          />
        </div>
      ))}
    </div>
  );
}
