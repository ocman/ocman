// @vitest-environment jsdom
import { beforeEach, expect, it, vi } from 'vitest';
import { fireEvent, render, screen } from '@testing-library/react';
import { MemoryRouter } from 'react-router-dom';
import { SessionComposerSlot } from './SessionComposerSlot';
import { useFactoryGraphIssues, useResolveFactoryRecoveryGate, useWorkEpics } from '../../lib/queries';
import { FactoryRecoveryActions } from '../../components/FactoryRecoveryActions';

vi.mock('../../lib/queries', () => ({ useFactoryGraphIssues: vi.fn(), useResolveFactoryRecoveryGate: vi.fn(), useWorkEpics: vi.fn() }));
vi.mock('../../components/FactoryPlanApproval', () => ({ FactoryPlanApproval: () => null }));
vi.mock('../../components/assistant/Composer', () => ({ Composer: () => <div>Conversation composer</div> }));
vi.mock('../../components/LaunchProgressCard', () => ({ LaunchProgressCard: () => null }));

const gate = { issueId: 'gate', epicId: 'epic', attemptId: 'stuck-attempt', workId: 'work', question: 'Which API?', reason: 'Unsafe to guess', choices: ['A', 'B'], resolution: 'open' };
const session = { platform: 'r-owner:opencode', id: 'stuck-session' };
const mutate = vi.fn();

beforeEach(() => {
  vi.clearAllMocks();
  vi.mocked(useWorkEpics).mockReturnValue({ refetch: vi.fn(), data: [{ id: 'epic', attempts: [{ id: 'stuck-attempt', workId: 'work', session }, { id: 'new-attempt', workId: 'work', session: { ...session, id: 'new-session' } }] }] } as never);
  vi.mocked(useFactoryGraphIssues).mockReturnValue([{ data: [{ id: 'gate', recovery: gate }] }] as never);
  vi.mocked(useResolveFactoryRecoveryGate).mockReturnValue({ mutate } as never);
});

function show(platformId = session.platform, sessionId = session.id) {
  return render(<MemoryRouter><SessionComposerSlot sessionId={sessionId} platformId={platformId} factoryEpicID="" firstUnreadMessageId={null} unreadMessageCount={0} onJumpToUnread={vi.fn()} permission={null} question={null} composer={{ isRunning: false } as never} /></MemoryRouter>);
}

it('shows recovery choices beside the stuck conversation without a Factory query parameter', () => {
  show();
  expect(screen.getByText('Which API?')).toBeInTheDocument();
  expect(screen.getByText('Conversation composer')).toBeInTheDocument();
  fireEvent.change(screen.getByLabelText('Recovery response'), { target: { value: 'B' } });
  fireEvent.click(screen.getByRole('button', { name: 'Resume work' }));
  expect(mutate).toHaveBeenCalledWith({ id: 'gate', action: 'resume', response: 'B' });
});

it.each([['opencode', session.id], [session.platform, 'new-session']])('does not show the gate on another owner or attempt: %s/%s', (platform, id) => {
  show(platform, id);
  expect(screen.queryByText('Which API?')).not.toBeInTheDocument();
});

it.each(['resume', 'retry', 'cancel'])('hides a resolved %s gate', (resolution) => {
  vi.mocked(useFactoryGraphIssues).mockReturnValue([{ data: [{ id: 'gate', recovery: { ...gate, resolution } }] }] as never);
  show();
  expect(screen.queryByText('Which API?')).not.toBeInTheDocument();
});

it('accepts a free-text recovery response', () => {
  vi.mocked(useFactoryGraphIssues).mockReturnValue([{ data: [{ id: 'gate', recovery: { ...gate, choices: [] } }] }] as never);
  show();
  fireEvent.change(screen.getByLabelText('Recovery response'), { target: { value: 'Use the existing API' } });
  fireEvent.click(screen.getByRole('button', { name: 'Resume work' }));
  expect(mutate).toHaveBeenCalledWith({ id: 'gate', action: 'resume', response: 'Use the existing API' });
});

it('links to the exact stuck attempt rather than the newest attempt', () => {
  const attempts = [{ id: gate.attemptId, workId: gate.workId, phase: 'active', session }, { id: 'new-attempt', workId: gate.workId, phase: 'active', session: { ...session, id: 'new-session' } }];
  render(<MemoryRouter><FactoryRecoveryActions gate={gate} attempts={attempts} inbox /></MemoryRouter>);
  expect(screen.getByRole('link', { name: 'Inspect recovery session' })).toHaveAttribute('href', '/session/stuck-session?factoryEpic=epic');
});

it('does not invent a session link when the attempt has no session', () => {
  render(<MemoryRouter><FactoryRecoveryActions gate={gate} attempts={[]} /></MemoryRouter>);
  expect(screen.queryByRole('link', { name: 'Inspect recovery session' })).not.toBeInTheDocument();
});

it('keeps pending resume limited to its saved response', () => {
  vi.mocked(useFactoryGraphIssues).mockReturnValue([{ data: [{ id: 'gate', recovery: { ...gate, response: 'B', resolution: 'resume_pending' } }] }] as never);
  show();
  expect(screen.getByLabelText('Recovery response')).toBeDisabled();
  expect(screen.getByLabelText('Recovery response')).toHaveValue('B');
  expect(screen.queryByRole('button', { name: 'Retry work' })).not.toBeInTheDocument();
  expect(screen.queryByRole('button', { name: 'Cancel work' })).not.toBeInTheDocument();
  fireEvent.click(screen.getByRole('button', { name: 'Resume work' }));
  expect(mutate).toHaveBeenCalledWith({ id: 'gate', action: 'resume', response: 'B' });
});

it.each(['epics', 'issues'])('lets the user retry failed %s loading', (source) => {
  const refetch = vi.fn();
  if (source === 'epics') {
    vi.mocked(useWorkEpics).mockReturnValue({ isError: true, refetch } as never);
    vi.mocked(useFactoryGraphIssues).mockReturnValue([]);
  }
  else vi.mocked(useFactoryGraphIssues).mockReturnValue([{ isError: true, refetch }] as never);
  show();
  expect(screen.getByRole('alert')).toHaveTextContent('Could not load Factory recovery.');
  expect(screen.getByRole('alert')).toHaveClass('oc-error-banner', 'oc-error-banner--compact');
  expect(screen.getByRole('alert')).not.toHaveClass('factory-plan-approval');
  fireEvent.click(screen.getByRole('button', { name: 'Retry' }));
  expect(refetch).toHaveBeenCalled();
});

it.each(['epics', 'issues'])('disables retry while failed %s loading is being retried', (source) => {
  if (source === 'epics') {
    vi.mocked(useWorkEpics).mockReturnValue({ isError: true, isFetching: true, refetch: vi.fn() } as never);
    vi.mocked(useFactoryGraphIssues).mockReturnValue([]);
  }
  else vi.mocked(useFactoryGraphIssues).mockReturnValue([{ isError: true, isFetching: true, refetch: vi.fn() }] as never);
  show();
  expect(screen.getByRole('button', { name: 'Retry' })).toBeDisabled();
  expect(screen.getByRole('button', { name: 'Retry' })).toHaveAttribute('aria-busy', 'true');
});
