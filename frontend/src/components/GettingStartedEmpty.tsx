import { useUiStore } from '../lib/uiStore';

/**
 * First-run / empty-state guidance. Shown wherever the session or
 * project list is genuinely empty (no OpenCode history yet). Explains
 * the data model — ocman surfaces sessions OpenCode produces — and
 * gives the one actionable next step: open the command palette to
 * launch a session in a project directory.
 *
 * `compact` trims the copy for the narrow sidebar; the full variant is
 * used in the projects table.
 */
export function GettingStartedEmpty({ compact = false }: { compact?: boolean }) {
  const openProjectPalette = useUiStore((s) => s.openProjectPalette);

  return (
    <div
      data-testid="getting-started-empty"
      style={{
        display: 'flex',
        flexDirection: 'column',
        gap: 10,
        padding: compact ? '20px 16px' : 24,
        color: 'var(--text-dim)',
        textAlign: compact ? 'left' : 'center',
        alignItems: compact ? 'stretch' : 'center',
        maxWidth: compact ? undefined : 520,
        margin: compact ? undefined : '0 auto',
        lineHeight: 1.5,
      }}
    >
      <strong style={{ color: 'var(--text)' }}>No sessions yet</strong>
      <p style={{ margin: 0 }}>
        ocman shows the coding sessions OpenCode creates. Start one here, or
        run <code>opencode</code> in any project directory and it will appear
        in this list.
      </p>
      <button
        type="button"
        className="vscode-btn"
        onClick={openProjectPalette}
        style={{ alignSelf: compact ? 'flex-start' : 'center', padding: '6px 12px', fontSize: 13 }}
      >
        + New session
      </button>
      <p style={{ margin: 0, fontSize: 12 }}>
        Tip: press <kbd>Alt</kbd>+<kbd>N</kbd> any time to start a session.
      </p>
    </div>
  );
}
