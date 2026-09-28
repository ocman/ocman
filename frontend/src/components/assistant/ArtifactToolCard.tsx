import { Link } from 'react-router-dom';
import type { CreatedArtifact } from '../../lib/artifactsApi';

export function ArtifactToolCard({ artifact }: { artifact: CreatedArtifact }) {
  return (
    <div className="oc-read-line" data-testid="artifact-tool-card">
      <i className="bi bi-archive" aria-hidden="true" />
      <Link to={`/artifacts/${encodeURIComponent(artifact.id)}`}>{artifact.title}</Link>
      <span className="artifact-muted">{`${artifact.items} item${artifact.items === 1 ? '' : 's'}`}</span>
    </div>
  );
}
