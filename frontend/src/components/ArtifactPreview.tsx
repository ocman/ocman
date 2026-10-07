import { useEffect, useState } from 'react';
import { previewKind, type ArtifactItem } from '../lib/artifactsApi';
import { Button } from './Control';
import { MarkdownContent } from './assistant/MarkdownText';

/**
 * Inline preview of one artifact file; renders nothing for unsupported types.
 * interactiveUrl, when given, lets the user opt an HTML file into running its
 * scripts (see HtmlPreview); shared views never pass it.
 */
export function ArtifactPreview({ item, interactiveUrl }: { item: ArtifactItem; interactiveUrl?: string }) {
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
  if (kind === 'html') return <HtmlPreview url={item.url} interactiveUrl={interactiveUrl} title={item.name ?? 'HTML preview'} />;
  if (error) return <p className="artifact-error">Preview unavailable.</p>;
  if (text === undefined) return <p className="artifact-muted">Loading preview...</p>;
  if (kind === 'markdown') return <div className="artifact-preview-markdown oc-md" data-testid="artifact-preview-markdown"><MarkdownContent text={text} /></div>;
  return <pre className="artifact-preview-text" data-testid="artifact-preview-text">{text}</pre>;
}

/**
 * Static by default: an empty sandbox (opaque origin, no scripts), matching the
 * file's `Content-Security-Policy: sandbox`. Run scripts swaps to the
 * interactive URL, whose response CSP grants exactly the one capability this
 * frame does: allow-scripts, never allow-same-origin. Opting in is per file and
 * remembered in this browser until Stop scripts.
 */
function HtmlPreview({ url, interactiveUrl, title }: { url: string; interactiveUrl?: string; title: string }) {
  // This visit's choice per URL; the stored opt-in applies until one is made.
  const [chosen, setChosen] = useState<Record<string, boolean>>({});
  const live = !!interactiveUrl && (chosen[interactiveUrl] ?? scriptsAllowed(interactiveUrl));
  return <>
    <div className="artifact-preview-toolbar">
      <span className="artifact-muted">{live
        ? 'Scripts are running in an isolated sandbox without network access or access to ocman.'
        : `Scripts are disabled in this preview.${interactiveUrl ? '' : ' Download the file to use it interactively.'}`}</span>
      {interactiveUrl && <Button type="button" size="small" variant="ghost" onClick={() => { allowScripts(interactiveUrl, !live); setChosen({ ...chosen, [interactiveUrl]: !live }); }}>
        <i className={`bi ${live ? 'bi-stop-circle' : 'bi-play-circle'}`} aria-hidden="true" />{live ? 'Stop scripts' : 'Run scripts'}
      </Button>}
    </div>
    <iframe key={live ? 'live' : 'static'} className="artifact-preview-html" src={live ? interactiveUrl : url}
      sandbox={live ? 'allow-scripts' : ''} title={title} data-testid="artifact-preview-html" />
  </>;
}

const SCRIPTS_KEY = 'ocman:artifact-scripts:';

function scriptsAllowed(url: string): boolean {
  try { return localStorage.getItem(SCRIPTS_KEY + url) === '1'; } catch { return false; }
}

// ponytail: a deleted artifact leaves its key behind; ids are never reused, so it is only a few stray bytes.
function allowScripts(url: string, on: boolean) {
  try {
    if (on) localStorage.setItem(SCRIPTS_KEY + url, '1'); else localStorage.removeItem(SCRIPTS_KEY + url);
  } catch { /* storage unavailable: the choice lasts for this visit */ }
}
