// @vitest-environment jsdom
import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen, fireEvent, waitFor } from '@testing-library/react';
import { LaunchSplitButton } from './LaunchSplitButton';
import * as api from '../../lib/upstreamApi';
import { useApiStore } from '../../lib/apiStore';
import userEvent from '@testing-library/user-event';

beforeEach(() => {
  vi.restoreAllMocks();
  useApiStore.setState({ recentSessions: [], recentSessionsHash: '' });
});

describe('LaunchSplitButton', () => {
  it('closes on keyboard selection and disables both controls until launch settles', async () => {
    const user = userEvent.setup();
    let finish!: (result: Awaited<ReturnType<typeof api.postHandle>>) => void;
    const spy = vi.spyOn(api, 'postHandle').mockImplementation(() => new Promise((resolve) => { finish = resolve; }));
    render(<LaunchSplitButton directory="/repo" remoteId="box" remote="origin" type="pr" number={42} crossFork={false} />);
    screen.getByRole('button', { name: 'More launch options' }).focus();
    await user.keyboard('{ArrowDown}{End}{Enter}');
    expect(spy).toHaveBeenCalledExactlyOnceWith({
      dir: '/repo', remoteId: 'box', remote: 'origin', type: 'pr', number: 42,
      mode: 'session', action: 'review', fetchHead: false,
    });
    expect(screen.queryByRole('menu')).not.toBeInTheDocument();
    expect(screen.getByTestId('launch-default')).toBeDisabled();
    expect(screen.getByRole('button', { name: 'More launch options' })).toBeDisabled();
    finish({ childSessionId: 'review-1', mode: 'session', platform: 'r-box:opencode', remoteId: 'box' });
    await waitFor(() => expect(screen.getByTestId('launch-default')).toBeEnabled());
    expect(useApiStore.getState().recentSessions[0]?.id).toBe('review-1');
  });

  it('dismisses launch options with Escape and restores trigger focus', async () => {
    const user = userEvent.setup();
    const spy = vi.spyOn(api, 'postHandle');
    render(<LaunchSplitButton directory="/repo" remoteId="local" remote="origin" type="issue" number={9} crossFork={false} />);
    const trigger = screen.getByRole('button', { name: 'More launch options' });
    await user.click(trigger);
    expect(screen.getAllByRole('menuitem')).toHaveLength(1);
    await user.keyboard('{Escape}');
    await waitFor(() => expect(trigger).toHaveFocus());
    expect(screen.queryByRole('menu')).not.toBeInTheDocument();
    expect(spy).not.toHaveBeenCalled();
  });

  it('preserves review mode through cross-fork confirmation', async () => {
    const spy = vi.spyOn(api, 'postHandle')
      .mockRejectedValueOnce(new api.UpstreamApiError({
        error: { code: 'requires_fetch', message: 'Fetch required', fetchTarget: 'ocman/pr-42' },
      }, 409))
      .mockResolvedValue({ childSessionId: 'review-worktree', mode: 'worktree', platform: 'opencode', remoteId: 'local' });
    render(<LaunchSplitButton directory="/repo" remoteId="local" remote="origin" type="pr" number={42} crossFork />);
    fireEvent.keyDown(screen.getByRole('button', { name: 'More launch options' }), { key: 'ArrowDown' });
    fireEvent.click(screen.getByTestId('launch-review-worktree'));
    fireEvent.click(await screen.findByRole('button', { name: 'Confirm' }));
    await waitFor(() => expect(spy).toHaveBeenLastCalledWith(expect.objectContaining({
      mode: 'worktree', action: 'review', fetchHead: true,
    })));
    expect(screen.queryByRole('menu')).not.toBeInTheDocument();
  });

  it('seeds the new session into the sidebar and shows a Launched confirmation', async () => {
    vi.spyOn(api, 'postHandle').mockResolvedValue({
      childSessionId: 'child-1',
      mode: 'session',
      platform: 'r-box:opencode',
      remoteId: 'box',
    });

    render(
      <LaunchSplitButton
        directory="/repo"
        remoteId="box"
        remote="origin"
        type="pr"
        number={42}
        crossFork={false}
      />,
    );

    fireEvent.click(screen.getByTestId('launch-default'));

    await waitFor(() => {
      expect(screen.getByTestId('launch-default')).toHaveTextContent('Launched ✓');
    });

    const recent = useApiStore.getState().recentSessions;
    expect(recent[0]?.id).toBe('child-1');
    expect(recent[0]?.title).toBe('pr #42');
    expect(recent[0]?.platform).toBe('r-box:opencode');
    expect(recent[0]?.remoteId).toBe('box');
    expect(api.postHandle).toHaveBeenCalledWith(expect.objectContaining({ remoteId: 'box' }));
  });

  it('does not seed a session for worktree mode', async () => {
    vi.spyOn(api, 'postHandle').mockResolvedValue({
      childSessionId: 'child-2',
      mode: 'worktree',
      platform: 'opencode',
      remoteId: 'local',
    });

    render(
      <LaunchSplitButton
        directory="/repo"
        remoteId="local"
        remote="origin"
        type="pr"
        number={7}
        crossFork={false}
      />,
    );

    fireEvent.keyDown(screen.getByTestId('launch-menu-toggle'), { key: 'ArrowDown' });
    fireEvent.click(screen.getByTestId('launch-worktree'));

    await waitFor(() => {
      expect(api.postHandle).toHaveBeenCalled();
    });
    expect(useApiStore.getState().recentSessions).toHaveLength(0);
  });

  it('offers review options for a PR and sends action:review', async () => {
    const spy = vi.spyOn(api, 'postHandle').mockResolvedValue({
      childSessionId: 'child-3',
      mode: 'worktree',
      platform: 'opencode',
      remoteId: 'local',
    });

    render(
      <LaunchSplitButton
        directory="/repo"
        remoteId="local"
        remote="origin"
        type="pr"
        number={11}
        crossFork={false}
      />,
    );

    fireEvent.keyDown(screen.getByTestId('launch-menu-toggle'), { key: 'ArrowDown' });
    fireEvent.click(screen.getByTestId('launch-review-worktree'));

    await waitFor(() => {
      expect(spy).toHaveBeenCalledWith(
        expect.objectContaining({ type: 'pr', number: 11, mode: 'worktree', action: 'review' }),
      );
    });
  });

  it('does not offer review options for an issue', () => {
    render(
      <LaunchSplitButton
        directory="/repo"
        remoteId="local"
        remote="origin"
        type="issue"
        number={5}
        crossFork={false}
      />,
    );

    fireEvent.keyDown(screen.getByTestId('launch-menu-toggle'), { key: 'ArrowDown' });
    expect(screen.queryByTestId('launch-review-worktree')).toBeNull();
    expect(screen.queryByTestId('launch-review-session')).toBeNull();
  });

  it('surfaces an error when the handle fails', async () => {
    vi.spyOn(api, 'postHandle').mockRejectedValue(new Error('boom'));

    render(
      <LaunchSplitButton
        directory="/repo"
        remoteId="local"
        remote="origin"
        type="issue"
        number={9}
        crossFork={false}
      />,
    );

    fireEvent.click(screen.getByTestId('launch-default'));

    await waitFor(() => {
      expect(screen.getByRole('alert')).toHaveTextContent('boom');
    });
  });

  it('surfaces a warning when the session was created without its prompt', async () => {
    vi.spyOn(api, 'postHandle').mockResolvedValue({
      childSessionId: 'child-4', mode: 'session', platform: 'opencode', remoteId: 'local',
      promptError: 'initial prompt was not sent',
    });
    render(
      <LaunchSplitButton directory="/repo" remoteId="local" remote="origin" type="issue" number={9} crossFork={false} />,
    );

    fireEvent.click(screen.getByTestId('launch-default'));

    await waitFor(() => expect(screen.getByRole('alert')).toHaveTextContent('initial prompt was not sent'));
    expect(useApiStore.getState().recentSessions[0]?.id).toBe('child-4');
  });
});
