import { useCallback, useEffect, useRef, useState } from 'react';
import { useNavigate } from 'react-router-dom';
import './WorktreeFormModal.css';
import { api } from '../lib/api';
import type { Project } from '../lib/api';
import { useApiStore } from '../lib/apiStore';
import { useUiStore } from '../lib/uiStore';
import { useWorktreeSessions } from '../lib/useCapabilities';

// Submit progress states surfaced to the user. The modal stays open
// across all of them so submit feels like a single waiting step rather
// than a series of redirects.
type SubmitStage =
  | 'idle'
  | 'creating-worktree'; // POST /api/worktree/create-and-launch in flight
                         // (the backend creates the worktree, ensures the
                         // project instance, and creates the in-app session)

/**
 * WorktreeFormModal collects the inputs needed to create a worktree
 * session and drives the POST /api/worktree/create-and-launch flow.
 *
 * The form is intentionally a separate modal rather than a palette
 * sub-mode (AD-6): a 3-field form with validation messaging is easier
 * to author and style outside the palette's single-field UX.
 *
 * Submission flow:
 *   1. Validate locally; submit POST.
 *   2. Show "Creating worktree…" while in-flight.
 *   3. On success, switch tmux to the returned session and close.
 *   4. On 4xx, surface the error inline; leave the form open.
 *
 * Implementation note: the outer `WorktreeFormModal` handles the
 * open/close gate and capability check. The inner `WorktreeForm` is
 * rendered with a `key` that increments on each open transition, so
 * React unmounts/remounts it — giving fresh `useState` defaults
 * without calling setState inside an effect
 * (react-hooks/set-state-in-effect).
 */
export function WorktreeFormModal() {
  const open = useUiStore((s) => s.worktreeFormOpen);
  const gen = useUiStore((s) => s.worktreeFormGen);
  const initialProject = useUiStore((s) => s.worktreeFormProject);
  const initialBranch = useUiStore((s) => s.worktreeFormBranch);
  const close = useUiStore((s) => s.closeWorktreeForm);
  const allowed = useWorktreeSessions();

  if (!open) return null;

  if (!allowed) {
    // Defensive: the modal should never be opened when the feature is
    // gated off, but if some other code path triggers it (e.g. via
    // useUiStore.getState() in dev tools), surface a clear message
    // rather than failing on the API call.
    return (
      <div className="oc-wt-backdrop" onClick={close}>
        <div className="oc-wt-modal" onClick={(e) => e.stopPropagation()}>
          <header><h2>Worktree sessions unavailable</h2></header>
          <div className="oc-wt-body">
            <p>
              The /wt feature requires git, tmux, and opencode on PATH,
              plus an OpenCode platform adapter registered.
            </p>
          </div>
          <footer><button type="button" onClick={close}>Close</button></footer>
        </div>
      </div>
    );
  }

  // `key={gen}` forces React to unmount/remount WorktreeForm on each
  // open transition, giving fresh useState defaults without calling
  // setState inside an effect (react-hooks/set-state-in-effect).
  return (
    <WorktreeForm
      key={gen}
      initialProject={initialProject}
      initialBranch={initialBranch}
      close={close}
    />
  );
}

// ---------------------------------------------------------------------------
// Inner form — remounted on each open transition via `key`.
// ---------------------------------------------------------------------------

interface WorktreeFormProps {
  initialProject: string | undefined;
  initialBranch: string | undefined;
  close: () => void;
}

function WorktreeForm({ initialProject, initialBranch, close }: WorktreeFormProps) {
  const projectsLoader = useApiStore((s) => s.getProjects);
  const seedNewSession = useApiStore((s) => s.seedNewSession);
  const navigate = useNavigate();

  // Fresh defaults on every mount (which happens on every open
  // transition thanks to the `key` prop on this component).
  const [projectDir, setProjectDir] = useState<string>(initialProject ?? '');
  const [projectList, setProjectList] = useState<Project[]>([]);
  const [branch, setBranch] = useState(initialBranch ?? '');
  const [newBranch, setNewBranch] = useState(true);
  const [baseRef, setBaseRef] = useState('');
  const [stage, setStage] = useState<SubmitStage>('idle');
  const [error, setError] = useState<string | null>(null);
  const [warning, setWarning] = useState<string | null>(null);
  const branchInputRef = useRef<HTMLInputElement>(null);

  const submitting = stage !== 'idle';

  // Focus the branch input and load the project list on mount.
  useEffect(() => {
    requestAnimationFrame(() => branchInputRef.current?.focus());
    if (!initialProject) {
      projectsLoader().then(setProjectList).catch(() => setProjectList([]));
    }
  }, [initialProject, projectsLoader]);

  // Once we know which project we're working with, fetch its default
  // base ref to pre-fill the input. setState inside `.then()` is fine
  // — the lint rule only flags *synchronous* setState in the effect body.
  useEffect(() => {
    if (!projectDir) return;
    const ctrl = new AbortController();
    api.worktree
      .defaultBaseRef(projectDir, ctrl.signal)
      .then((r) => setBaseRef(r.baseRef))
      .catch(() => {
        // Non-fatal: leave the field empty; user can fill it in.
      });
    return () => ctrl.abort();
  }, [projectDir]);

  const handleClose = useCallback(() => {
    if (submitting) return; // refuse to close mid-submit
    close();
  }, [submitting, close]);

  // ESC closes the modal (when not submitting).
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (e.key === 'Escape') {
        e.preventDefault();
        handleClose();
      }
    };
    window.addEventListener('keydown', onKey);
    return () => window.removeEventListener('keydown', onKey);
  }, [handleClose]);

  const onSubmit = async (e: React.FormEvent) => {
    e.preventDefault();
    setError(null);
    if (!projectDir) {
      setError('Please pick a project.');
      return;
    }
    if (!branch.trim()) {
      setError('Branch name is required.');
      return;
    }
    if (newBranch && !baseRef.trim()) {
      setError('Base ref is required when creating a new branch.');
      return;
    }

    setStage('creating-worktree');
    let resp: Awaited<ReturnType<typeof api.worktree.createAndLaunch>>;
    try {
      resp = await api.worktree.createAndLaunch({
        projectDir,
        branch: branch.trim(),
        newBranch,
        baseRef: newBranch ? baseRef.trim() : undefined,
      });
    } catch (err) {
      setStage('idle');
      const msg = err instanceof Error ? err.message : String(err);
      setError(msg);
      return;
    }

    // Backend fell back to checking out a pre-existing branch
    // because one with that name already existed locally. Surface
    // it as a non-blocking notice and pause briefly so the user
    // notices before we navigate away.
    if (resp.branchExisted) {
      setWarning(
        `Branch "${branch.trim()}" already existed — reusing it instead of creating a new one.`,
      );
      await new Promise((r) => setTimeout(r, 1500));
    }

    // #268: the backend already created the in-app session on the
    // project's single opencode instance, rooted at the worktree, and
    // returned its ID. Seed it into the store and navigate straight to
    // it — no tmux switch, no separate createSession round-trip.
    setStage('idle');
    close();
    if (resp.sessionId) {
      seedNewSession(resp.sessionId, resp.worktreePath, '', branch.trim());
      navigate(`/session/${resp.sessionId}`);
    } else {
      navigate(`/project/${encodeURIComponent(resp.worktreePath)}`);
    }
  };

  const stageLabel = stage === 'creating-worktree'
    ? 'Creating worktree session…'
    : null;

  return (
    <div className="oc-wt-backdrop" onClick={handleClose}>
      <form className="oc-wt-modal" onClick={(e) => e.stopPropagation()} onSubmit={onSubmit}>
        <header>
          <h2>New worktree session</h2>
          <kbd className="oc-wt-kbd">ESC</kbd>
        </header>

        <div className="oc-wt-body">
          {/* Project: read-only when pre-filled, dropdown when not */}
          <label className="oc-wt-field">
            <span>Project</span>
            {initialProject ? (
              <input
                type="text"
                value={initialProject}
                readOnly
                aria-label="Project directory"
              />
            ) : (
              <select
                value={projectDir}
                onChange={(e) => setProjectDir(e.target.value)}
                disabled={submitting}
                aria-label="Pick a project"
              >
                <option value="">— pick a project —</option>
                {projectList.map((p) => (
                  <option key={p.directory} value={p.directory}>
                    {p.directory}
                  </option>
                ))}
              </select>
            )}
          </label>

          <label className="oc-wt-field">
            <span>Branch</span>
            <input
              ref={branchInputRef}
              type="text"
              value={branch}
              onChange={(e) => setBranch(e.target.value)}
              placeholder="feature/login"
              disabled={submitting}
              autoComplete="off"
              spellCheck={false}
            />
          </label>

          <label className="oc-wt-checkbox">
            <input
              type="checkbox"
              checked={newBranch}
              onChange={(e) => setNewBranch(e.target.checked)}
              disabled={submitting}
            />
            <span>Create new branch</span>
          </label>

          {newBranch && (
            <label className="oc-wt-field">
              <span>Base ref</span>
              <input
                type="text"
                value={baseRef}
                onChange={(e) => setBaseRef(e.target.value)}
                placeholder="main"
                disabled={submitting}
                autoComplete="off"
                spellCheck={false}
              />
            </label>
          )}

          {error && <div className="oc-wt-error" role="alert">{error}</div>}
          {warning && (
            <div
              className="oc-wt-warning"
              role="status"
              data-testid="worktree-warning"
            >
              {warning}
            </div>
          )}
          {submitting && stageLabel && (
            <div className="oc-wt-spinner" aria-live="polite">
              {stageLabel}
            </div>
          )}
        </div>

        <footer>
          <button
            type="button"
            onClick={handleClose}
            disabled={submitting}
            className="oc-wt-btn oc-wt-btn--secondary"
          >
            Cancel
          </button>
          <button
            type="submit"
            disabled={submitting || !branch.trim() || !projectDir}
            className="oc-wt-btn oc-wt-btn--primary"
          >
            {submitting ? 'Creating…' : 'Create & launch'}
          </button>
        </footer>
      </form>
    </div>
  );
}
