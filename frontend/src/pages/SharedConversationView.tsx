import { useEffect, useState } from 'react';
import { useParams } from 'react-router-dom';
import { api, sharedExportMarkdownUrl, type SharedConversation } from '../lib/api';
import { OcmanRuntimeProvider } from '../components/OcmanRuntimeProvider';
import { AssistantThread } from '../components/AssistantThread';
import { ErrorBoundary } from '../components/ErrorBoundary';
import { PrintCollapseContext } from '../lib/printCollapseContext';
import './SharedConversationView.css';
import { mergeRelayChunks, readRelayShare, relayKeyFromFragment, relayPollMs } from '../lib/relayShare';

type LoadState =
  | { status: 'loading' }
  | { status: 'error'; message: string }
  | { status: 'ready'; data: SharedConversation };

/**
 * SharedConversationView is the public, read-only rendering of a shared
 * conversation. It is mounted OUTSIDE the auth gate and the app chrome,
 * so it works for visitors who aren't logged into ocman — the share
 * token in the URL is the only credential.
 *
 * It reuses the same OcmanRuntimeProvider + AssistantThread rendering
 * pipeline as the authenticated session view, but with no composer and
 * no live updates: `canSend` is false and there's no SSE stream. A
 * print stylesheet (SharedConversationView.css) hides the toolbar so
 * the browser's "Save as PDF" produces a clean document.
 */
export function SharedConversationView({ relay = false }: { relay?: boolean }) {
  const { token } = useParams<{ token: string }>();
  const [state, setState] = useState<LoadState>(
    token ? { status: 'loading' } : { status: 'error', message: 'Missing share token.' },
  );
  // Default to collapsed tool outputs so the printed PDF stays compact;
  // the reader can expand everything via the toolbar checkbox.
  const [collapseTools, setCollapseTools] = useState(true);

  useEffect(() => {
    if (!token) return;
    const controller = new AbortController();
    if (relay) {
      const key = relayKeyFromFragment();
      if (!key) {
        queueMicrotask(() => setState({ status: 'error', message: 'Missing share decryption key.' }));
        return;
      }
      let current: SharedConversation | null = null;
      let next = 0;
      const poll = async () => {
        try {
          const result = await readRelayShare(token, key, next, controller.signal);
          if (result.chunks.length > 0) {
            current = mergeRelayChunks(current, result.chunks);
            next = result.last + 1;
            setState({ status: 'ready', data: current });
          }
        } catch (err) {
          if (err instanceof DOMException && err.name === 'AbortError') return;
          setState({ status: 'error', message: 'Failed to load or decrypt the shared conversation.' });
        }
      };
      void poll();
      const timer = window.setInterval(() => void poll(), relayPollMs);
      return () => {
        window.clearInterval(timer);
        controller.abort();
      };
    }
    // setState calls below run inside async callbacks (not synchronously
    // in the effect body), which is the supported pattern for
    // synchronizing with an external data source.
    api
      .sharedConversation(token, controller.signal)
      .then((data) => setState({ status: 'ready', data }))
      .catch((err) => {
        if (err instanceof DOMException && err.name === 'AbortError') return;
        const message =
          err instanceof Error && /404|not found/i.test(err.message)
            ? 'This share link is no longer available. It may have been revoked or expired.'
            : 'Failed to load the shared conversation.';
        setState({ status: 'error', message });
      });
    return () => controller.abort();
  }, [relay, token]);

  useEffect(() => {
    if (state.status === 'ready') {
      const title = state.data.session?.title?.trim();
      document.title = title ? `${title} · Shared conversation` : 'Shared conversation';
    } else {
      document.title = 'Shared conversation';
    }
  }, [state]);

  if (state.status === 'loading') {
    return (
      <div className="oc-shared-view" data-testid="shared-loading">
        <div className="oc-shared-loading">Loading shared conversation…</div>
      </div>
    );
  }

  if (state.status === 'error') {
    return (
      <div className="oc-shared-view">
        <div className="oc-shared-error" role="alert" data-testid="shared-error">
          {state.message}
        </div>
      </div>
    );
  }

  const { session, messages, parts } = state.data;
  const title = session?.title?.trim() || 'Shared conversation';

  return (
    <div
      className={`oc-shared-view${collapseTools ? ' oc-collapse-tools' : ''}`}
      data-testid="shared-conversation"
    >
      <header className="oc-shared-header">
        <div className="oc-shared-title-block">
          <h1 className="oc-shared-title">{title}</h1>
          <span className="oc-shared-badge">read-only</span>
        </div>
        <div className="oc-shared-toolbar">
          <label className="oc-shared-toggle" data-testid="shared-collapse-tools">
            <input
              type="checkbox"
              checked={collapseTools}
              onChange={(e) => setCollapseTools(e.target.checked)}
            />
            Collapse tool outputs
          </label>
          {!relay && <a
            className="oc-shared-action"
            href={token ? sharedExportMarkdownUrl(token) : '#'}
            download
            data-testid="shared-download-md"
          >
            Download Markdown
          </a>}
          {relay && (
            <a
              className="oc-shared-action"
              href={`http://127.0.0.1:8228/import-share?url=${encodeURIComponent(window.location.href)}`}
              data-testid="shared-fork-local"
            >
              Fork in local ocman
            </a>
          )}
          <button
            type="button"
            className="oc-shared-action"
            onClick={() => window.print()}
            data-testid="shared-print-pdf"
          >
            Print / Save as PDF
          </button>
        </div>
      </header>

      <main className="oc-shared-thread">
        <ErrorBoundary name="shared:thread" inline resetKey={session?.id ?? token ?? ''}>
          <PrintCollapseContext.Provider value={collapseTools}>
            <OcmanRuntimeProvider
              key={session?.id ?? token}
              messages={messages}
              parts={parts}
              sessionId={session?.id ?? ''}
              canSend={false}
              projectDirectory={session?.directory}
            >
              <AssistantThread />
            </OcmanRuntimeProvider>
          </PrintCollapseContext.Provider>
        </ErrorBoundary>
      </main>
    </div>
  );
}
