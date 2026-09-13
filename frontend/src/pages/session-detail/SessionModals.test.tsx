// @vitest-environment jsdom

import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import { beforeAll, beforeEach, describe, expect, it, vi } from 'vitest';
import type { Message, Part, Project, Session } from '../../lib/api';

vi.mock('../../lib/api', () => ({
  api: {
    forkSession: vi.fn().mockResolvedValue({ id: 'sess-forked' }),
    moveSession: vi.fn().mockResolvedValue(undefined),
  },
}));
const patchRecentSession = vi.fn();
vi.mock('../../lib/apiStore', () => ({
  useApiStore: (selector: (s: Record<string, unknown>) => unknown) => selector({ patchRecentSession }),
}));
vi.mock('../../lib/remoteLog', () => ({ remoteLog: { error: vi.fn() } }));
vi.mock('./RenameModal', () => ({
  RenameModal: ({ onRenamed }: { onRenamed: (t: string) => void }) => (
    <button onClick={() => onRenamed('Renamed')}>rename-now</button>
  ),
}));

import { api } from '../../lib/api';
import { SessionModals, type SessionModalsProps } from './SessionModals';

const session = { id: 's1', title: 'Old', directory: '/current', remoteId: 'local' } as Session;
const messages: Message[] = [
  { id: 'msg-user-1', sessionId: 's1', timeCreated: 1_700_000_000_000, data: { role: 'user' } },
];
const parts: Part[] = [
  { id: 'p1', messageId: 'msg-user-1', sessionId: 's1', data: { type: 'text', text: 'First prompt' } },
];
const historyMessages: Message[] = [
  { id: 'msg-old', sessionId: 's1', timeCreated: 1_600_000_000_000, data: { role: 'user' } },
  ...messages,
];
const historyParts: Part[] = [
  { id: 'p0', messageId: 'msg-old', sessionId: 's1', data: { type: 'text', text: 'Ancient prompt' } },
  ...parts,
];

function renderModals(over: Partial<SessionModalsProps> = {}) {
  const props: SessionModalsProps = {
    session,
    messages,
    parts,
    allProjects: [
      { directory: '/proj-local', remoteId: 'local' } as Project,
      { directory: '/proj-remote', remoteId: 'r1' } as Project,
    ],
    recentSessions: [{ id: 's2', directory: '/recent-local' } as Session],
    messageJumpHistory: null,
    pending: { pending: null, begin: vi.fn(), fail: vi.fn(), clear: vi.fn(), observeMessages: vi.fn() },
    showRenameModal: false,
    setShowRenameModal: vi.fn(),
    showForkPicker: false,
    setShowForkPicker: vi.fn(),
    showMessageJumpPicker: false,
    setShowMessageJumpPicker: vi.fn(),
    showMovePicker: false,
    setShowMovePicker: vi.fn(),
    showMovePathDialog: false,
    setShowMovePathDialog: vi.fn(),
    patchSession: vi.fn(),
    navigateToSession: vi.fn(),
    hydrateHistory: vi.fn(),
    onRenamed: vi.fn(),
    onScrollToMessage: vi.fn(),
    ...over,
  };
  render(<SessionModals {...props} />);
  return props;
}

beforeAll(() => {
  Element.prototype.scrollIntoView = vi.fn();
});
beforeEach(() => vi.clearAllMocks());

describe('SessionModals', () => {
  it('renders nothing when every flag is off', () => {
    renderModals();
    expect(document.body.textContent).toBe('');
  });

  it('patches title locally and in the sidebar after a rename', () => {
    const p = renderModals({ showRenameModal: true });
    fireEvent.click(screen.getByText('rename-now'));
    expect(p.patchSession).toHaveBeenCalledWith({ title: 'Renamed' });
    expect(patchRecentSession).toHaveBeenCalledWith('s1', { title: 'Renamed' });
    expect(p.onRenamed).toHaveBeenCalled();
  });

  it('forks and navigates to the new session', async () => {
    const p = renderModals({ showForkPicker: true });
    fireEvent.click(screen.getByText('First prompt'));
    expect(p.setShowForkPicker).toHaveBeenCalledWith(false);
    expect(p.pending.begin).toHaveBeenCalledWith('/fork');
    expect(api.forkSession).toHaveBeenCalledWith('s1', 'msg-user-1');
    await waitFor(() => expect(p.navigateToSession).toHaveBeenCalledWith('sess-forked'));
    expect(p.pending.clear).toHaveBeenCalled();
  });

  it('hydrates history before jumping to a message not in the live window', () => {
    const p = renderModals({
      showMessageJumpPicker: true,
      messageJumpHistory: { sessionId: 's1', messages: historyMessages, parts: historyParts },
    });
    fireEvent.click(screen.getByText('Ancient prompt'));
    expect(p.hydrateHistory).toHaveBeenCalledWith(historyMessages, historyParts);
    expect(p.onScrollToMessage).toHaveBeenCalledWith('msg-old');
  });

  it('lists same-host directories and moves the session', async () => {
    const p = renderModals({ showMovePicker: true });
    expect(screen.getByText('/proj-local')).toBeInTheDocument();
    expect(screen.getByText('/recent-local')).toBeInTheDocument();
    expect(screen.queryByText('/proj-remote')).toBeNull();

    fireEvent.click(screen.getByText('/proj-local'));
    expect(p.setShowMovePicker).toHaveBeenCalledWith(false);
    expect(api.moveSession).toHaveBeenCalledWith('s1', '/proj-local');
    await waitFor(() => expect(p.patchSession).toHaveBeenCalledWith({ directory: '/proj-local' }));
    expect(patchRecentSession).toHaveBeenCalledWith('s1', { directory: '/proj-local' });
  });

  it('reports a failed move on the pending bubble', async () => {
    vi.mocked(api.moveSession).mockRejectedValueOnce(new Error('nope'));
    const p = renderModals({ showMovePathDialog: true });
    fireEvent.change(screen.getByLabelText('Project directory'), { target: { value: '/custom' } });
    fireEvent.click(screen.getByRole('button', { name: 'Move' }));
    expect(p.setShowMovePathDialog).toHaveBeenCalledWith(false);
    await waitFor(() => expect(p.pending.fail).toHaveBeenCalledWith('nope'));
  });
});
