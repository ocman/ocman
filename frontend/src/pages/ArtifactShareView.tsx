import { useEffect, useState } from 'react';
import { ArtifactPreview } from '../components/ArtifactPreview';
import { MarkdownContent } from '../components/assistant/MarkdownText';
import { formatBytes } from '../lib/artifactsApi';
import type { ArtifactShare } from '../lib/artifactShare';
import '../components/Artifacts.css';

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

  return (
    <div className="oc-shared-view" data-testid="shared-artifact">
      <header className="oc-shared-header">
        <div className="oc-shared-title-block">
          <h1 className="oc-shared-title">{artifact.title}</h1>
          <span className="oc-shared-badge">shared artifact</span>
        </div>
      </header>
      <main className="artifact-page">
        {artifact.description && <section className="artifact-description"><MarkdownContent text={artifact.description} /></section>}
        {artifact.links.length > 0 && (
          <section aria-label="Links"><h3>Links</h3><ul>
            {artifact.links.map((l, i) => <li key={i}><a href={l.url} target="_blank" rel="noopener noreferrer">{l.label || l.url}</a></li>)}
          </ul></section>
        )}
        {artifact.files.length > 0 && (
          <section aria-label="Files" className="artifact-files"><h3>Files</h3>
            {artifact.files.map((f, i) => (
              <article key={i} className="artifact-file" data-testid="artifact-file">
                <header>
                  <strong>{f.name}</strong>
                  <span className="artifact-muted">{f.mime} · {formatBytes(f.size)}</span>
                  {urls[i] && <a href={urls[i]} download={f.name} aria-label={`Download ${f.name}`}>Download</a>}
                </header>
                {urls[i] && <ArtifactPreview item={{ kind: 'file', name: f.name, mime: f.mime, size: f.size, url: urls[i] }} />}
              </article>
            ))}
          </section>
        )}
      </main>
    </div>
  );
}
