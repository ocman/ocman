import { Link } from 'react-router-dom';
import type { CreatedArtifact } from '../../lib/artifactsApi';
import styles from './ArtifactToolCard.module.css';

export function ArtifactToolCard({ artifact }: { artifact: CreatedArtifact }) {
  return (
    <div className="oc-read-line" data-testid="artifact-tool-card">
      <i className="bi bi-box-seam" aria-hidden="true" />
      <Link to={`/artifacts/${encodeURIComponent(artifact.id)}`}>{artifact.title}</Link>
      <span className={styles.metadata}>{`${artifact.items} item${artifact.items === 1 ? '' : 's'}`}</span>
    </div>
  );
}
