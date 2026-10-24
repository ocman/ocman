import { useContext, useEffect, useRef, useState } from 'react';
import { useDocumentVisible } from './usePanelVisible';
import { PR_CHECKS_REFRESH_EVENT } from './prChecksCache';
import { PREVIEW_AUTH_EVENT, PreviewOwnerContext, PreviewSessionContext, loadPreviewConfig, mayPreview, resolvePreviews } from './previews';
import type { PreviewConfig, PreviewProvider, PreviewResult } from './previews';

// The request batcher adds 20 ms, keeping the total initial delay at 300 ms.
const RESOLVE_DELAY_MS = 280;

interface Resolved {
  text: string;
  owner: string;
  session: string;
  previews: PreviewResult[];
  providers: PreviewProvider[];
  refreshChecks: boolean;
}


/**
 * Resolves `text` into provider previews. The server holds every
 * credential; the browser only knows which providers are configured and
 * which link hosts they preview, so text that cannot produce a preview is
 * never sent.
 */
export function useProviderPreviews(text: string, openGraph = false): { previews: PreviewResult[]; providers: PreviewProvider[]; loading: boolean; refreshChecks: boolean; refresh: () => void } {
  const visible = useDocumentVisible();
  const owner = useContext(PreviewOwnerContext);
  const session = useContext(PreviewSessionContext);
  const [resolved, setResolved] = useState<Resolved | null>(null);
  const [pending, setPending] = useState(false);
  const [generation, setGeneration] = useState(0);
  const refreshPending = useRef(false);
  const refresh = () => {
    refreshPending.current = true;
    setGeneration((g) => g + 1);
  };
  // Keep matching identity available to the event listener during refresh.
  const lastResolved = useRef<Resolved | null>(null);

  useEffect(() => {
    const reset = () => { setResolved(null); setGeneration((g) => g + 1); };
    const refresh = (event: Event) => {
      const repositories = (event as CustomEvent<string[]>).detail;
      const previous = lastResolved.current;
      const known = previous?.text === text && previous.owner === owner && previous.session === session ? previous.previews : [];
      // Raw canonical links also match before the first lookup has finished.
      if (repositories && !repositories.some((repo) => text.includes(`https://${repo}/`)) && !known.some((preview) => {
        if (preview.kind !== 'pr' || !preview.url) return false;
        try {
          return repositories.includes(`${new URL(preview.url).host}/${preview.id.split('#')[0]}`);
        } catch { return false; }
      })) return;
      refreshPending.current = true;
      setGeneration((g) => g + 1);
    };
    window.addEventListener(PREVIEW_AUTH_EVENT, reset);
    window.addEventListener(PR_CHECKS_REFRESH_EVENT, refresh);
    return () => { window.removeEventListener(PREVIEW_AUTH_EVENT, reset); window.removeEventListener(PR_CHECKS_REFRESH_EVENT, refresh); };
  }, [text, owner, session]);

  useEffect(() => {
    if (!visible) return;
    const abort = new AbortController();
    const timer = setTimeout(() => {
      // Without app access the catalog is refused, but public forge links
      // may still resolve, so any https link is worth one call.
      loadPreviewConfig(owner).catch((): PreviewConfig | null => null).then(async (config) => {
        const worth = openGraph ? /^https?:\/\//.test(text) : config ? mayPreview(text, config) : /https:\/\//.test(text);
        if (!worth || abort.signal.aborted) return;
        // Public forge links resolve silently, as their cards always did.
        if (config?.providers.some((p) => p.accounts.length)) setPending(true);
        const refresh = refreshPending.current;
        const previews = openGraph
          ? await resolvePreviews(text, owner, abort.signal, refresh, true)
          : await resolvePreviews(text, owner, abort.signal, refresh);
        if (!abort.signal.aborted) {
          if (refresh) refreshPending.current = false;
          const result = { text, owner, session, previews, providers: config?.providers ?? [], refreshChecks: refresh };
          lastResolved.current = result;
          setResolved(result);
        }
      }).catch(() => {
        // Keep an existing card on refresh failure; initial failures use plain links.
      }).finally(() => {
        if (!abort.signal.aborted) setPending(false);
      });
    }, refreshPending.current ? 0 : RESOLVE_DELAY_MS);
    return () => { clearTimeout(timer); abort.abort(); };
  }, [text, owner, session, generation, visible, openGraph]);

  // Never show results for other text or another owner while reloading.
  const current = resolved && resolved.text === text && resolved.owner === owner && resolved.session === session ? resolved : null;
  return { previews: current?.previews ?? [], providers: current?.providers ?? [], loading: pending && !current, refreshChecks: current?.refreshChecks ?? false, refresh };
}

export const hasRichPreview = (p: PreviewResult) => !!p.title && (p.state === 'ok' || p.stale);

/** States that explain why a link has no rich card. */
export const NOTICE_STATES: ReadonlySet<PreviewResult['state']> = new Set(['connect', 'expired', 'denied']);

/** Whether the preview renders more than its plain link. */
export const needsCard = (p: PreviewResult) => hasRichPreview(p) || NOTICE_STATES.has(p.state) || !!p.choices?.length;
