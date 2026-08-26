// @vitest-environment jsdom
import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen, fireEvent, waitFor } from '@testing-library/react';
import { LaunchSplitButton } from './LaunchSplitButton';
import * as api from '../../lib/upstreamApi';
import { useApiStore } from '../../lib/apiStore';

beforeEach(() => {
  vi.restoreAllMocks();
  useApiStore.setState({ recentSessions: [], recentSessionsHash: '' });
});

describe('LaunchSplitButton', () => {
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

    fireEvent.click(screen.getByTestId('launch-menu-toggle'));
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

    fireEvent.click(screen.getByTestId('launch-menu-toggle'));
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

    fireEvent.click(screen.getByTestId('launch-menu-toggle'));
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
