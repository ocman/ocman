import { useEffect, useState } from 'react';
import { previewKind, type ArtifactItem } from '../lib/artifactsApi';
import { MarkdownContent } from './assistant/MarkdownText';

/** Inline preview of one artifact file; renders nothing for unsupported types. */
export function ArtifactPreview({ item }: { item: ArtifactItem }) {
  const kind = previewKind(item);
  const [loaded, setLoaded] = useState<{ url: string; text?: string; error?: boolean }>();
  const current = loaded?.url === item.url ? loaded : undefined;
  const text = current?.text;
  const error = current?.error;

  useEffect(() => {
    if (!item.url || (kind !== 'markdown' && kind !== 'text')) return;
    const url = item.url;
    const ctrl = new AbortController();
    fetch(url, { signal: ctrl.signal })
      .then((r) => (r.ok ? r.text() : Promise.reject(new Error(String(r.status)))))
      .then((body) => setLoaded({ url, text: body }), (err) => { if (!ctrl.signal.aborted) { console.warn('artifact preview failed', err); setLoaded({ url, error: true }); } });
    return () => ctrl.abort();
  }, [item.url, kind]);

  if (!item.url || kind === 'none') return null;
  if (kind === 'image') return <img className="artifact-preview-image" src={item.url} alt={item.name ?? ''} data-testid="artifact-preview-image" />;
  if (error) return <p className="artifact-missing">Preview unavailable.</p>;
  if (text === undefined) return <p className="artifact-muted">Loading preview...</p>;
  if (kind === 'markdown') return <div className="artifact-preview-markdown" data-testid="artifact-preview-markdown"><MarkdownContent text={text} /></div>;
  return <pre className="artifact-preview-text" data-testid="artifact-preview-text">{text}</pre>;
}
