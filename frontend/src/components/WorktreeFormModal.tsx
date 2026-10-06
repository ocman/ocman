import { useCallback, useEffect, useRef, useState } from 'react';
import { useNavigate } from 'react-router-dom';
import styles from './WorktreeFormModal.module.css';
import { api } from '../lib/api';
import type { Project } from '../lib/api';
import { useApiStore } from '../lib/apiStore';
import { useUiStore } from '../lib/uiStore';
import { useOpencodeLaunch } from '../lib/useCapabilities';
import { Modal } from './Modal';
import { ModalHeader } from './ModalHeader';
import { ModalFooter } from './ModalFooter';
import { Button, TextField } from './Control';
import { CheckboxField } from './CheckboxField';
import { InlineAlert } from './InlineAlert';
import { SearchSelect } from './SearchSelect';

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
 *   3. On success, open the returned session and close.
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
  const parentSessionId = useUiStore((s) => s.worktreeFormParentSessionId);
  const remoteId = useUiStore((s) => s.worktreeFormRemoteId);
  const close = useUiStore((s) => s.closeWorktreeForm);
  const allowed = useOpencodeLaunch(remoteId);

  if (!open) return null;

  if (!allowed) {
    // Defensive: the modal should never be opened when the feature is
    // gated off, but if some other code path triggers it (e.g. via
    // useUiStore.getState() in dev tools), surface a clear message
    // rather than failing on the API call.
    return (
      <Modal
        label="Worktree sessions unavailable"
        onClose={close}
      >
        <ModalHeader title="Worktree sessions unavailable" onClose={close} closeLabel="Close unavailable worktree dialog" />
        <div className={styles.body}>
          <p>
            The /wt feature requires git, tmux, and opencode on PATH,
            plus an OpenCode platform adapter registered.
          </p>
        </div>
        <ModalFooter label="Unavailable worktree actions"><Button type="button" onClick={close}>Close</Button></ModalFooter>
      </Modal>
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
      parentSessionId={parentSessionId}
      remoteId={remoteId}
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
  parentSessionId: string | undefined;
  /** Owning machine of the project; undefined lets the backend infer it. */
  remoteId: string | undefined;
  close: () => void;
}

function WorktreeForm({ initialProject, initialBranch, parentSessionId, remoteId, close }: WorktreeFormProps) {
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
  const [baseRefs, setBaseRefs] = useState<string[]>([]);
  const [refError, setRefError] = useState<string | null>(null);
  const [refRevision, setRefRevision] = useState(0);
  const [stage, setStage] = useState<SubmitStage>('idle');
  const [error, setError] = useState<string | null>(null);
  const [warning, setWarning] = useState<string | null>(null);
  // Number of always-allow permissions the child would inherit from the
  // parent session (#101). null = unknown/not applicable (hint hidden).
  const [inheritCount, setInheritCount] = useState<number | null>(null);
  const branchInputRef = useRef<HTMLInputElement>(null);

  const submitting = stage !== 'idle';
  const owner = remoteId || 'local';
  const projectOptions = [...new Set([
    projectDir,
    ...projectList.filter((project) => (project.remoteId || 'local') === owner).map((project) => project.directory),
  ].filter(Boolean))].map((value) => ({ value, label: value }));

  // Focus the branch input and load the project list on mount.
  useEffect(() => {
    requestAnimationFrame(() => branchInputRef.current?.focus());
    projectsLoader().then(setProjectList).catch(() => setProjectList([]));
  }, [projectsLoader]);

  // Once we know which project we're working with, fetch its default
  // base ref to pre-fill the input. setState inside `.then()` is fine
  // — the lint rule only flags *synchronous* setState in the effect body.
  useEffect(() => {
    if (!projectDir) return;
    const ctrl = new AbortController();
    Promise.all([
      api.worktree.defaultBaseRef(projectDir, owner, ctrl.signal),
      api.gitBranches(projectDir, ctrl.signal, owner).catch(() => ({ branches: [] })),
    ])
      .then(([result, { branches }]) => {
        if (ctrl.signal.aborted) return;
        setBaseRef(result.baseRef);
        setBaseRefs([...new Set([result.baseRef, ...branches].filter(Boolean))]);
        setRefError(null);
      })
      .catch(() => {
        if (!ctrl.signal.aborted) setRefError('Could not load base refs.');
      });
    return () => ctrl.abort();
  }, [projectDir, owner, refRevision]);

  // When launched from a session, look up how many always-allow
  // permissions would be inherited (#101) so the form can show a hint.
  // Hidden when the inherit setting is off or the count is 0.
  useEffect(() => {
    if (!parentSessionId) return;
    const ctrl = new AbortController();
    (async () => {
      try {
        const { enabled } = await api.getWorktreeInheritPermissions(ctrl.signal);
        if (!enabled) {
          setInheritCount(null);
          return;
        }
        const perms = await api.approvedPermissions(parentSessionId);
        setInheritCount(perms.length);
      } catch {
        // Non-fatal: the hint just won't show.
        setInheritCount(null);
      }
    })();
    return () => ctrl.abort();
  }, [parentSessionId]);

  const handleClose = useCallback(() => {
    if (submitting) return; // refuse to close mid-submit
    close();
  }, [submitting, close]);

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
        parentSessionId,
        remoteId: owner,
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
      const platform = resp.platform ?? '';
      seedNewSession(resp.sessionId, resp.worktreePath, platform, branch.trim(), resp.remoteId ?? owner);
      navigate(`/session/${encodeURIComponent(resp.sessionId)}${platform ? `?platform=${encodeURIComponent(platform)}` : ''}`);
    } else {
      navigate(`/project/${encodeURIComponent(resp.worktreePath)}`);
    }
  };

  const stageLabel = stage === 'creating-worktree'
    ? 'Creating worktree session…'
    : null;

  return (
    <Modal
      label="New worktree session"
      onClose={handleClose}
      canClose={!submitting}
    >
      <form className={styles.form} onSubmit={onSubmit}>
        <ModalHeader title="New worktree session" onClose={handleClose} canClose={!submitting} closeLabel="Close worktree session dialog" />

        <div className={styles.body}>
          <div className={styles.field}>
            <span>Project</span>
            <SearchSelect ariaLabel="Project" searchLabel="Search projects" placeholder="Pick a project"
              value={projectDir} title={projectDir} options={projectOptions} disabled={submitting} onChange={(directory) => {
                if (directory === projectDir) return;
                setProjectDir(directory); setBaseRef(''); setBaseRefs([]); setRefError(null);
              }} />
          </div>

          <label className={styles.field}>
            <span>Branch</span>
            <TextField
              ref={branchInputRef}
              data-autofocus
              type="text"
              value={branch}
              onChange={(e) => setBranch(e.target.value)}
              placeholder="feature/login"
              disabled={submitting}
              autoComplete="off"
              spellCheck={false}
            />
          </label>

          <CheckboxField
            label="Create new branch"
            checked={newBranch}
            onChange={(e) => setNewBranch(e.target.checked)}
            disabled={submitting}
          />

          {newBranch && (
            <div className={styles.field}>
              <span>Base ref</span>
              <SearchSelect ariaLabel="Base ref" searchLabel="Search base refs" placeholder="Select a base ref"
                value={baseRef} options={baseRefs.map((value) => ({ value, label: value }))}
                onChange={setBaseRef} disabled={submitting || baseRefs.length === 0} />
            </div>
          )}

          {newBranch && refError && <InlineAlert onRetry={() => setRefRevision((revision) => revision + 1)}>{refError}</InlineAlert>}

          {inheritCount !== null && inheritCount > 0 && (
            <div className={styles.hint} data-testid="worktree-inherit-hint">
              Will inherit {inheritCount} approved{' '}
              {inheritCount === 1 ? 'permission' : 'permissions'} from the current session.
            </div>
          )}

          {error && <InlineAlert><span className={styles.message}>{error}</span></InlineAlert>}
          {warning && (
            <div
              className={styles.warning}
              role="status"
              data-testid="worktree-warning"
            >
              {warning}
            </div>
          )}
          {submitting && stageLabel && (
            <div className={styles.progress} aria-live="polite">
              {stageLabel}
            </div>
          )}
        </div>

        <ModalFooter label="Create worktree actions">
          <Button
            type="button"
            onClick={handleClose}
            disabled={submitting}
          >
            Cancel
          </Button>
          <Button
            type="submit"
            disabled={submitting || !branch.trim() || !projectDir}
            variant="accent"
            aria-busy={submitting}
          >
            {submitting ? 'Creating…' : 'Create & launch'}
          </Button>
        </ModalFooter>
      </form>
    </Modal>
  );
}
