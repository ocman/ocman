import { useNavigate } from 'react-router-dom';
import type { Session } from '../lib/api';
import { formatNumber, formatDuration, relativeTime, shortPath } from '../lib/format';
import { StatusBadge } from './StatusBadge';


interface Props {
  sessions: Session[];
  showProject?: boolean;
  loading?: boolean;
}

export function SessionTable({ sessions, showProject, loading }: Props) {
  const navigate = useNavigate();

  if (loading) {
    return (
      <div className="oc-list-loading">
        <div className="oc-spinner" />
        Loading sessions...
      </div>
    );
  }

  if (!sessions.length) {
    return (
      <table>
        <tbody>
          <tr>
            <td colSpan={showProject ? 9 : 8} style={{ textAlign: 'center', color: 'var(--text-dim)', padding: 24 }}>
              No sessions found
            </td>
          </tr>
        </tbody>
      </table>
    );
  }

  return (
    <table>
      <thead>
        <tr>
          <th>Session</th>
          <th>Status</th>
          {showProject && <th>Project</th>}
          <th>Messages</th>
          <th>Duration</th>
          <th>Changes</th>
          <th>Tokens (in/out)</th>
          <th>Started</th>
        </tr>
      </thead>
      <tbody>
        {sessions.map(s => (
          <tr
            key={s.id}
            className={s.hasPort ? '' : 'no-port'}
            onClick={() => navigate(`/session/${s.id}`)}
          >
            <td>
              <div style={{ color: 'var(--accent)', fontWeight: 500 }}>{s.title || 'Untitled'}</div>
              <div className="mono">{s.id}</div>
            </td>
            <td><StatusBadge status={s.status} /></td>
            {showProject && <td className="mono">{shortPath(s.directory)}</td>}
            <td>{s.messageCount}</td>
            <td className="mono">{formatDuration(s.durationMs)}</td>
            <td>
              {s.summaryFiles ? <span className="tag tag-files">{s.summaryFiles} files</span> : null}
              {' '}
              {s.summaryAdditions ? <span className="tag tag-additions">+{s.summaryAdditions}</span> : null}
              {' '}
              {s.summaryDeletions ? <span className="tag tag-deletions">-{s.summaryDeletions}</span> : null}
            </td>
            <td className="mono">{formatNumber(s.totalInputTokens)} / {formatNumber(s.totalOutputTokens)}</td>
            <td>{relativeTime(s.timeCreated)}</td>
          </tr>
        ))}
      </tbody>
    </table>
  );
}
