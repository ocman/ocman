import { useUiStore } from '../lib/uiStore';
import { Button } from './Control';
import styles from './GettingStartedEmpty.module.css';

/**
 * First-run / empty-state guidance. Shown wherever the session or
 * project list is genuinely empty (no OpenCode history yet). Explains
 * the data model — ocman surfaces sessions OpenCode produces — and
 * gives the one actionable next step: add a project directory.
 *
 * `compact` trims the copy for the narrow sidebar; the full variant is
 * used in the projects table.
 */
export function GettingStartedEmpty({ compact = false }: { compact?: boolean }) {
  const openProjectPalette = useUiStore((s) => s.openProjectPalette);

  return (
    <div
      data-testid="getting-started-empty"
      className={`${styles.root}${compact ? ` ${styles.compact}` : ''}`}
    >
      <strong className={styles.heading}>No sessions yet</strong>
      <p className={styles.copy}>
        ocman shows the coding sessions OpenCode creates. Add a project
        directory here, or run <code>opencode</code> in any project directory
        and it will appear in this list.
      </p>
      <Button
        type="button"
        variant="accent"
        className={styles.action}
        onClick={openProjectPalette}
      >
        + New project
      </Button>
      <p className={styles.tip}>
        Tip: press <kbd>Alt</kbd>+<kbd>N</kbd> any time to start a session in a known project.
      </p>
    </div>
  );
}
