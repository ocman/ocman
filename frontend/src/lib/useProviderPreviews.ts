import { useContext, useEffect, useRef, useState } from 'react';
import { PR_CHECKS_REFRESH_EVENT } from './prChecksCache';
import { PREVIEW_AUTH_EVENT, PreviewOwnerContext, loadPreviewConfig, mayPreview, resolvePreviews } from './previews';
import type { PreviewConfig, PreviewProvider, PreviewResult } from './previews';

const RESOLVE_DELAY_MS = 300;

interface Resolved {
  text: string;
  owner: string;
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
export function useProviderPreviews(text: string): { previews: PreviewResult[]; providers: PreviewProvider[]; loading: boolean; refreshChecks: boolean } {
  const owner = useContext(PreviewOwnerContext);
  const [resolved, setResolved] = useState<Resolved | null>(null);
  const [pending, setPending] = useState(false);
  const [generation, setGeneration] = useState(0);
  const refreshPending = useRef(false);
  // Keep matching identity while display state is cleared for a refresh.
  const lastResolved = useRef<Resolved | null>(null);

  useEffect(() => {
    const reset = () => { setResolved(null); setGeneration((g) => g + 1); };
    const refresh = (event: Event) => {
      const repositories = (event as CustomEvent<string[]>).detail;
      const previous = lastResolved.current;
      const known = previous?.text === text && previous.owner === owner ? previous.previews : [];
      // Raw canonical links also match before the first lookup has finished.
      if (repositories && !repositories.some((repo) => text.includes(`https://${repo}/`)) && !known.some((preview) => {
        if (preview.kind !== 'pr' || !preview.url) return false;
        try {
          return repositories.includes(`${new URL(preview.url).host}/${preview.id.split('#')[0]}`);
        } catch { return false; }
      })) return;
      refreshPending.current = true; reset();
    };
    window.addEventListener(PREVIEW_AUTH_EVENT, reset);
    window.addEventListener(PR_CHECKS_REFRESH_EVENT, refresh);
    return () => { window.removeEventListener(PREVIEW_AUTH_EVENT, reset); window.removeEventListener(PR_CHECKS_REFRESH_EVENT, refresh); };
  }, [text, owner]);

  useEffect(() => {
    const abort = new AbortController();
    const timer = setTimeout(() => {
      // Without app access the catalog is refused, but public forge links
      // may still resolve, so any https link is worth one call.
      loadPreviewConfig(owner).catch((): PreviewConfig | null => null).then(async (config) => {
        const worth = config ? mayPreview(text, config) : /https:\/\//.test(text);
        if (!worth || abort.signal.aborted) return;
        // Public forge links resolve silently, as their cards always did.
        if (config?.providers.some((p) => p.accounts.length)) setPending(true);
        const refresh = refreshPending.current;
        refreshPending.current = false;
        const previews = await resolvePreviews(text, owner, abort.signal, refresh);
        if (!abort.signal.aborted) {
          const result = { text, owner, previews, providers: config?.providers ?? [], refreshChecks: refresh };
          lastResolved.current = result;
          setResolved(result);
        }
      }).catch(() => {
        // Safe fallback: plain links and custom rule cards still render.
        if (!abort.signal.aborted) setResolved(null);
      }).finally(() => {
        if (!abort.signal.aborted) setPending(false);
      });
    }, RESOLVE_DELAY_MS);
    return () => { clearTimeout(timer); abort.abort(); };
  }, [text, owner, generation]);

  // Never show results for other text or another owner while reloading.
  const current = resolved && resolved.text === text && resolved.owner === owner ? resolved : null;
  return { previews: current?.previews ?? [], providers: current?.providers ?? [], loading: pending && !current, refreshChecks: current?.refreshChecks ?? false };
}

export const hasRichPreview = (p: PreviewResult) => !!p.title && (p.state === 'ok' || p.stale);

/** States that explain why a link has no rich card. */
export const NOTICE_STATES: ReadonlySet<PreviewResult['state']> = new Set(['connect', 'expired', 'denied']);

/** Whether the preview renders more than its plain link. */
export const needsCard = (p: PreviewResult) => hasRichPreview(p) || NOTICE_STATES.has(p.state) || !!p.choices?.length;
