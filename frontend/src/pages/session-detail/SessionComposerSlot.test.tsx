// @vitest-environment jsdom
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { fireEvent, render, screen } from '@testing-library/react';

vi.mock('../../components/FactoryPlanApproval', () => ({ FactoryPlanApproval: () => null }));
vi.mock('../../components/FactorySessionRecovery', () => ({ FactorySessionRecovery: () => null }));
vi.mock('../../components/session/PermissionPrompt', () => ({ PermissionPrompt: ({ disabled, onReply }: { disabled?: boolean; onReply?: () => void }) =>
  <button disabled={disabled} onClick={() => onReply?.()}>permission-prompt</button> }));
vi.mock('../../components/session/QuestionPrompt', () => ({ QuestionPrompt: () => <div>question-prompt</div> }));
vi.mock('../../components/assistant/Composer', () => ({ Composer: () => <div>composer</div> }));

import { SessionComposerSlot, SessionPromptSlot, type SessionComposerSlotProps } from './SessionComposerSlot';
import type { ComponentProps } from 'react';
import { useFirstSubmission } from './firstSubmission';
import { useLaunchProgressStore } from '../../lib/launchProgressStore';

beforeEach(() => {
  useFirstSubmission.setState({ entries: {} });
  useLaunchProgressStore.getState().dismiss();
});

function renderSlot(over: Partial<SessionComposerSlotProps> = {}) {
  const props: SessionComposerSlotProps = {
    sessionId: 's1',
    platformId: 'opencode',
    factoryEpicID: '',
    firstUnreadMessageId: null,
    unreadMessageCount: 0,
    onJumpToUnread: vi.fn(),
    composer: null,
    ...over,
  };
  render(<SessionComposerSlot {...props} />);
  return props;
}

const permission = {} as NonNullable<ComponentProps<typeof SessionPromptSlot>['permission']>;
const question = {} as NonNullable<ComponentProps<typeof SessionPromptSlot>['question']>;
const composer = { isRunning: false } as NonNullable<SessionComposerSlotProps['composer']>;

describe('SessionComposerSlot', () => {
  it('keeps permission approval available during a pending first submission', () => {
    useFirstSubmission.setState({ entries: { s1: { text: '!ls', pending: true, execute: vi.fn() } } });
    const onReply = vi.fn();
    render(<SessionPromptSlot permission={{ ...permission, disabled: false, onReply }} question={null} />);
    renderSlot({ pendingPrompt: true, composer });
    const approve = screen.getByRole('button', { name: 'permission-prompt' });
    expect(approve).not.toBeDisabled();
    fireEvent.click(approve);
    expect(onReply).toHaveBeenCalledTimes(1);
  });
  it('renders permission over question over composer', () => {
    render(<SessionPromptSlot permission={permission} question={question} />);
    renderSlot({ pendingPrompt: true, composer });
    expect(screen.getByText('permission-prompt')).toBeInTheDocument();
    expect(screen.queryByText('composer')).toBeNull();
  });

  it('falls through to the question, then the composer, then nothing', () => {
    const { rerender, unmount } = render(<SessionPromptSlot permission={null} question={question} />);
    expect(screen.getByText('question-prompt')).toBeInTheDocument();
    rerender(<SessionPromptSlot permission={null} question={null} />);
    expect(screen.queryByText('question-prompt')).toBeNull();
    unmount();
    renderSlot({ composer });
    expect(screen.getByText('composer')).toBeInTheDocument();
  });

  it('shows the unread pill and jumps to the first unread message', () => {
    const p = renderSlot({ firstUnreadMessageId: 'm7', unreadMessageCount: 3 });
    const pill = screen.getByTestId('jump-to-first-unread');
    expect(pill).toHaveTextContent('3 new messages');
    fireEvent.click(pill);
    expect(p.onJumpToUnread).toHaveBeenCalledWith('m7');
  });

  it.each([
    ['running', () => undefined],
    ['failed', () => useLaunchProgressStore.getState().fail('boom')],
  ])('keeps a %s launch visible while a permission replaces the composer', (_, settle) => {
    useLaunchProgressStore.getState().begin('/repo', { remoteId: 'r1' });
    settle();
    render(<SessionPromptSlot permission={permission} question={null} />);
    renderSlot({ pendingPrompt: true, composer, directory: '/repo', remoteId: 'r1' });
    expect(screen.getByText('permission-prompt')).toBeInTheDocument();
    expect(screen.getByTestId('launch-progress')).toBeInTheDocument();
  });
});
