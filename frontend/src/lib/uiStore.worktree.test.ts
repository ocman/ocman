import { describe, it, expect, beforeEach } from 'vitest';
import { useUiStore } from './uiStore';

describe('uiStore worktree-form slice', () => {
  beforeEach(() => {
    // Clear modal state between tests so they don't leak.
    useUiStore.setState({
      worktreeFormOpen: false,
      worktreeFormProject: undefined,
      worktreeFormBranch: undefined,
      paletteOpen: false,
    });
  });

  it('starts closed by default', () => {
    const s = useUiStore.getState();
    expect(s.worktreeFormOpen).toBe(false);
    expect(s.worktreeFormProject).toBeUndefined();
    expect(s.worktreeFormBranch).toBeUndefined();
  });

  it('openWorktreeForm with no opts opens an unprefilled modal', () => {
    useUiStore.getState().openWorktreeForm();
    const s = useUiStore.getState();
    expect(s.worktreeFormOpen).toBe(true);
    expect(s.worktreeFormProject).toBeUndefined();
    expect(s.worktreeFormBranch).toBeUndefined();
  });

  it('openWorktreeForm passes the project through', () => {
    useUiStore.getState().openWorktreeForm({ projectDir: '/abs/path' });
    expect(useUiStore.getState().worktreeFormProject).toBe('/abs/path');
  });

  it('openWorktreeForm passes the branch through', () => {
    useUiStore.getState().openWorktreeForm({ projectDir: '/abs/path', branch: 'feature/login' });
    expect(useUiStore.getState().worktreeFormBranch).toBe('feature/login');
  });

  it('opening the worktree form closes the palette', () => {
    useUiStore.setState({ paletteOpen: true });
    useUiStore.getState().openWorktreeForm({ projectDir: '/abs' });
    expect(useUiStore.getState().paletteOpen).toBe(false);
  });

  it('closeWorktreeForm clears state', () => {
    useUiStore.getState().openWorktreeForm({ projectDir: '/abs' });
    useUiStore.getState().closeWorktreeForm();
    const s = useUiStore.getState();
    expect(s.worktreeFormOpen).toBe(false);
    expect(s.worktreeFormProject).toBeUndefined();
    expect(s.worktreeFormBranch).toBeUndefined();
  });
});
