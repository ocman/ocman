import { describe, it, expect } from 'vitest';
import { renderToStaticMarkup } from 'react-dom/server';
import { MemoryRouter } from 'react-router-dom';
import { WorktreeFormModal } from './WorktreeFormModal';
import { useUiStore } from '../lib/uiStore';

// We use renderToStaticMarkup which doesn't run effects and doesn't
// re-subscribe to Zustand stores after the initial render. That's
// enough to verify the closed-state contract: when worktreeFormOpen
// is false (the default), the modal must render nothing. The full
// open-form behaviour is exercised by:
//   - uiStore.worktree.test.ts  (open/close action contract)
//   - api.worktree.test.ts      (API client contract)
//   - manual smoke test         (the integrated UI flow)

describe('WorktreeFormModal', () => {
  it('renders nothing when closed', () => {
    useUiStore.setState({
      worktreeFormOpen: false,
      worktreeFormProject: undefined,
    });
    const html = renderToStaticMarkup(
      <MemoryRouter>
        <WorktreeFormModal />
      </MemoryRouter>,
    );
    expect(html).toBe('');
  });
});
