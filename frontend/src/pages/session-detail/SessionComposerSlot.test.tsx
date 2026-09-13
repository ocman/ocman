// @vitest-environment jsdom
import { describe, expect, it, vi } from 'vitest';
import { fireEvent, render, screen } from '@testing-library/react';

vi.mock('../../components/FactoryPlanApproval', () => ({ FactoryPlanApproval: () => null }));
vi.mock('../../components/session/PermissionPrompt', () => ({ PermissionPrompt: () => <div>permission-prompt</div> }));
vi.mock('../../components/session/QuestionPrompt', () => ({ QuestionPrompt: () => <div>question-prompt</div> }));
vi.mock('../../components/assistant/Composer', () => ({ Composer: () => <div>composer</div> }));

import { SessionComposerSlot, type SessionComposerSlotProps } from './SessionComposerSlot';

function renderSlot(over: Partial<SessionComposerSlotProps> = {}) {
  const props: SessionComposerSlotProps = {
    sessionId: 's1',
    platformId: 'opencode',
    factoryEpicID: '',
    firstUnreadMessageId: null,
    unreadMessageCount: 0,
    onJumpToUnread: vi.fn(),
    permission: null,
    question: null,
    composer: null,
    ...over,
  };
  render(<SessionComposerSlot {...props} />);
  return props;
}

const permission = {} as NonNullable<SessionComposerSlotProps['permission']>;
const question = {} as NonNullable<SessionComposerSlotProps['question']>;
const composer = { isRunning: false } as NonNullable<SessionComposerSlotProps['composer']>;

describe('SessionComposerSlot', () => {
  it('renders permission over question over composer', () => {
    renderSlot({ permission, question, composer });
    expect(screen.getByText('permission-prompt')).toBeInTheDocument();
    expect(screen.queryByText('composer')).toBeNull();
  });

  it('falls through to the question, then the composer, then nothing', () => {
    const { unmount } = render(<SessionComposerSlot
      sessionId="s1" platformId="p" factoryEpicID="" firstUnreadMessageId={null} unreadMessageCount={0}
      onJumpToUnread={vi.fn()} permission={null} question={question} composer={composer}
    />);
    expect(screen.getByText('question-prompt')).toBeInTheDocument();
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
});
