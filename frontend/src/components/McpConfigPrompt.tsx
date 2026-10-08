import { useEffect, useState } from 'react';
import * as Toast from '@radix-ui/react-toast';
import { api } from '../lib/api';
import type { McpConfigStatus } from '../lib/api.types';
import { remoteLog } from '../lib/remoteLog';
import { SubmitButton } from './Control';
import { PromptToast, PromptToastHeading, PromptToastBody, PromptToastActions, PromptToastClose, PromptToastViewport } from './PromptToast';

// Dismissal is persisted per *endpoint URL*, so moving the MCP port
// re-prompts once (the stored config is then stale) while a user who
// deliberately said "not now" isn't nagged on every page load.
const DISMISS_STORAGE_KEY = 'ocman.mcp-config-dismissed';

// localStorage access follows the repo convention (composerDraft.ts):
// wrapped in try/catch so a blocked Storage degrades to session-only
// dismissal instead of throwing.
function loadDismissed(): string | null {
  try {
    return window.localStorage.getItem(DISMISS_STORAGE_KEY);
  } catch {
    return null;
  }
}

function saveDismissed(url: string) {
  try {
    window.localStorage.setItem(DISMISS_STORAGE_KEY, url);
  } catch {
    // best-effort
  }
}

/**
 * McpConfigPrompt nags once when OpenCode's global config doesn't point
 * at ocman's MCP endpoint, and offers to write it.
 *
 * Mounted at the app root, checked once per page load. Renders nothing
 * in the common case (already registered, or dismissed for this URL).
 * When ocman won't rewrite the file — JSONC comments would be lost —
 * the toast shows the URL to add by hand instead of an Install button.
 */
export function McpConfigPrompt() {
  const [status, setStatus] = useState<McpConfigStatus | null>(null);
  const [dismissedUrl, setDismissedUrl] = useState<string | null>(loadDismissed);
  const [installing, setInstalling] = useState(false);
  const [installed, setInstalled] = useState<string | null>(null);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    let cancelled = false;
    api.getMcpConfig()
      .then((s) => { if (!cancelled) setStatus(s); })
      .catch((err) => remoteLog.error('mcp config status failed', err));
    return () => { cancelled = true; };
  }, []);

  if (!status || status.configured) return null;
  if (installed === null && dismissedUrl === status.wantUrl) return null;

  function dismiss() {
    saveDismissed(status!.wantUrl);
    setDismissedUrl(status!.wantUrl);
  }

  async function install() {
    setInstalling(true);
    setError(null);
    try {
      const res = await api.installMcpConfig();
      setInstalled(res.backupPath ? `Backup: ${res.backupPath}` : res.path);
    } catch (err) {
      setError(err instanceof Error ? err.message : 'install failed');
    } finally {
      setInstalling(false);
    }
  }

  // Already installed by this prompt: confirm, and say what's needed for
  // OpenCode to pick it up.
  if (installed !== null) {
    return (
      <Toast.Provider swipeDirection="right" duration={Infinity}>
        <PromptToast open duration={Infinity} onOpenChange={(open) => { if (!open) { dismiss(); setInstalled(null); } }}>
          <PromptToastClose />
          <PromptToastHeading>ocman MCP installed</PromptToastHeading>
          <PromptToastBody data-testid="mcp-config-installed">
            Restart OpenCode to load it. {installed}
          </PromptToastBody>
        </PromptToast>
        <PromptToastViewport />
      </Toast.Provider>
    );
  }

  const stale = !!status.currentUrl;
  return (
    <Toast.Provider swipeDirection="right" duration={Infinity}>
      <PromptToast
        data-kind="permission"
        open
        onOpenChange={(open) => { if (!open) dismiss(); }}
        duration={Infinity}
      >
        <PromptToastClose label="Not now" />
        <PromptToastHeading>
          {status.unavailable ? 'ocman MCP unavailable' : stale ? 'ocman MCP is out of date' : 'ocman MCP not configured'}
        </PromptToastHeading>
        <PromptToastBody data-testid="mcp-config-prompt">
          {status.unavailable ? status.reason : status.editable
            ? <>Register <code>{status.wantUrl}</code> in {status.path}?</>
            : <>Add <code>{status.wantUrl}</code> to {status.path} by hand — {status.reason}</>}
        </PromptToastBody>
        {error && (
          <PromptToastBody data-testid="mcp-config-error">
            {error}
          </PromptToastBody>
        )}
        {status.editable && (
          <PromptToastActions label="MCP setup actions">
            <Toast.Action asChild altText="Install the ocman MCP server">
              <SubmitButton type="button" size="small" variant="accent" pending={installing} pendingLabel="Installing…" onClick={(e) => {
                // Keep the toast open so the result is visible.
                e.preventDefault();
                return install();
              }}>Install</SubmitButton>
            </Toast.Action>
          </PromptToastActions>
        )}
      </PromptToast>
      <PromptToastViewport />
    </Toast.Provider>
  );
}
