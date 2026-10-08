import { Link } from 'react-router-dom';
import { artifactBytes, formatBytes, useKnownSessionIds, type Artifact } from '../lib/artifactsApi';
import { formatDateTimeShort } from '../lib/format';
import { DataTable } from './DataTable';
import { ProjectLabel } from './ProjectLabel';
import styles from './ArtifactList.module.css';

export function ArtifactSessionLink({ artifact, known }: { artifact: Artifact; known?: Set<string> }) {
  if (!artifact.sessionId) return <>-</>;
  if (known && !known.has(artifact.sessionId)) return <span className={styles.muted} title={artifact.sessionId}>missing</span>;
  const q = artifact.platform ? `?platform=${encodeURIComponent(artifact.platform)}` : '';
  return <Link to={`/session/${encodeURIComponent(artifact.sessionId)}${q}`} onClick={(e) => e.stopPropagation()}>Session</Link>;
}

/** Artifact rows; reused by the /artifacts page and the session sidebar. */
export function ArtifactList({ artifacts, compact = false }: { artifacts: Artifact[]; compact?: boolean }) {
  const known = useKnownSessionIds();
  return (
    <DataTable framed data-testid="artifact-list">
        <thead><tr><th>Title</th>{!compact && <th>Project</th>}{!compact && <th>Session</th>}<th>Items</th><th>Size</th><th>Created</th></tr></thead>
        <tbody>
          {artifacts.map((a) => (
            <tr key={a.id} data-testid="artifact-row">
              <td className={styles.titleCell}><Link className={styles.title} to={`/artifacts/${encodeURIComponent(a.id)}`}>{a.title}</Link></td>
              {!compact && <td><ProjectLabel path={a.directory} /></td>}
              {!compact && <td><ArtifactSessionLink artifact={a} known={known} /></td>}
              <td>{a.items.length}</td>
              <td>{formatBytes(artifactBytes(a))}</td>
              <td>{formatDateTimeShort(Date.parse(a.createdAt))}</td>
            </tr>
          ))}
        </tbody>
    </DataTable>
  );
}
