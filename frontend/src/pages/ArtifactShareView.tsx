import { useEffect, useState } from 'react';
import { ArtifactContent } from '../components/ArtifactContent';
import type { ArtifactShare } from '../lib/artifactShare';

/** Read-only rendering of an artifact decrypted from a relay share. */
export function ArtifactShareView({ artifact }: { artifact: ArtifactShare }) {
  const [urls, setUrls] = useState<string[]>([]);

  useEffect(() => {
    const created = artifact.files.map((f) => URL.createObjectURL(f.blob));
    // Object URLs are an external resource bound to this render's files.
    // eslint-disable-next-line react-hooks/set-state-in-effect
    setUrls(created);
    return () => created.forEach((u) => URL.revokeObjectURL(u));
  }, [artifact]);

  const files = artifact.files.map((f, i) => ({ kind: 'file' as const, name: f.name, mime: f.mime, size: f.size, url: urls[i] }));
  const links = artifact.links.map((l) => ({ kind: 'link' as const, url: l.url, label: l.label }));
  return (
    <div className="oc-shared-view" data-testid="shared-artifact">
      <header className="oc-shared-header">
        <div className="oc-shared-title-block">
          <h1 className="oc-shared-title">{artifact.title}</h1>
          <span className="oc-shared-badge">shared artifact</span>
        </div>
      </header>
      <main className="artifact-page">
        <ArtifactContent description={artifact.description} links={links} files={files} />
      </main>
    </div>
  );
}
