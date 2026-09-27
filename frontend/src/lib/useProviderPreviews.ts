import { useContext, useEffect, useState } from 'react';
import { PREVIEW_AUTH_EVENT, PreviewOwnerContext, loadPreviewProviders, resolvePreviews } from './previews';
import type { PreviewProvider, PreviewResult } from './previews';

const RESOLVE_DELAY_MS = 300;

interface Resolved {
  text: string;
  owner: string;
  previews: PreviewResult[];
  providers: PreviewProvider[];
}

/**
 * Resolves `text` into provider previews for this browser's viewer. State
 * lives only in the component: a connect/disconnect/sign-out event drops it
 * before reloading, so one viewer's private titles never render for another.
 */
export function useProviderPreviews(text: string): { previews: PreviewResult[]; providers: PreviewProvider[]; loading: boolean } {
  const owner = useContext(PreviewOwnerContext);
  const [resolved, setResolved] = useState<Resolved | null>(null);
  const [pending, setPending] = useState(false);
  const [generation, setGeneration] = useState(0);

  useEffect(() => {
    const reset = () => { setResolved(null); setGeneration((g) => g + 1); };
    window.addEventListener(PREVIEW_AUTH_EVENT, reset);
    return () => window.removeEventListener(PREVIEW_AUTH_EVENT, reset);
  }, []);

  useEffect(() => {
    const abort = new AbortController();
    const timer = setTimeout(() => {
      // Without private-preview access the provider list is refused, but
      // public forge links still resolve.
      loadPreviewProviders(owner).catch((): PreviewProvider[] => []).then(async (providers) => {
        if ((providers.length === 0 && !/https?:\/\//.test(text)) || abort.signal.aborted) return;
        // Public forge links alone resolve silently, as their cards always did.
        if (providers.length > 0) setPending(true);
        const previews = await resolvePreviews(text, owner, abort.signal);
        if (!abort.signal.aborted) setResolved({ text, owner, previews, providers });
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
  return { previews: current?.previews ?? [], providers: current?.providers ?? [], loading: pending && !current };
}

export const hasRichPreview = (p: PreviewResult) => !!p.title && (p.state === 'ok' || p.stale);

/** States that need the viewer to act: connect, reconnect or pick a workspace. */
export const ACTION_STATES: ReadonlySet<PreviewResult['state']> = new Set(['connect', 'expired', 'denied', 'ambiguous']);

/** Whether the preview renders more than its plain link. */
export const needsCard = (p: PreviewResult) => hasRichPreview(p) || ACTION_STATES.has(p.state);

