// @vitest-environment jsdom

import { act, render, screen, waitFor } from '@testing-library/react';
import { describe, expect, it, vi, beforeEach, afterEach } from 'vitest';
import { PermissionPrompt, type PendingPermission } from './PermissionPrompt';

const permission: PendingPermission = {
  permissionId: 'perm-1',
  permission: 'Write to /tmp/output.txt',
  patterns: [],
};

describe('PermissionPrompt', () => {
  it('handles allow-once hotkey even when the dialog is not focused', async () => {
    const onReply = vi.fn();
    render(<PermissionPrompt permission={permission} onReply={onReply} />);

    screen.getByRole('dialog', { name: 'Permission required' });
    document.body.focus();
    window.dispatchEvent(new KeyboardEvent('keydown', { key: 'a', bubbles: true }));

    await waitFor(() => expect(onReply).toHaveBeenCalledWith('once'));
  });

  it('handles reject hotkey even when the dialog is not focused', async () => {
    const onReply = vi.fn();
    render(<PermissionPrompt permission={permission} onReply={onReply} />);

    screen.getByRole('dialog', { name: 'Permission required' });
    document.body.focus();
    window.dispatchEvent(new KeyboardEvent('keydown', { key: 'r', bubbles: true }));

    await waitFor(() => expect(onReply).toHaveBeenCalledWith('reject'));
  });

  it('shows the concrete action from permission metadata', () => {
    render(
      <PermissionPrompt
        permission={{ ...permission, metadata: { command: 'git status --short' } }}
        onReply={vi.fn()}
      />,
    );

    expect(screen.getByTestId('permission-action')).toHaveTextContent('Action');
    expect(screen.getByText('git status --short')).toBeInTheDocument();
  });

  describe('auto-approve footer (server-driven)', () => {
    beforeEach(() => {
      vi.useFakeTimers();
    });

    afterEach(() => {
      vi.useRealTimers();
    });

    it('shows "starting…" immediately when auto-approve is on but pending event not yet received', () => {
      render(
        <PermissionPrompt
          permission={permission}
          onReply={vi.fn()}
          autoApproveCapable
          autoApproveEnabled
        />,
      );

      expect(screen.getByText(/Auto-approve on/)).toBeInTheDocument();
      expect(screen.getByText(/starting/)).toBeInTheDocument();
    });

    it('shows countdown in footer when judgeStartsAt is in the future', () => {
      const judgeStartsAt = Date.now() + 5000;
      render(
        <PermissionPrompt
          permission={permission}
          onReply={vi.fn()}
          autoApproveCapable
          autoApproveEnabled
          judgeStartsAt={judgeStartsAt}
        />,
      );

      // Advance past the first interval tick so countdownMs is populated.
      act(() => { vi.advanceTimersByTime(250); });
      expect(screen.getByTestId('auto-approve-countdown')).toHaveTextContent('5s');
      expect(screen.getByText(/starting in/)).toBeInTheDocument();
    });

    it('counts down as time passes', () => {
      const judgeStartsAt = Date.now() + 3000;
      render(
        <PermissionPrompt
          permission={permission}
          onReply={vi.fn()}
          autoApproveCapable
          autoApproveEnabled
          judgeStartsAt={judgeStartsAt}
        />,
      );

      act(() => { vi.advanceTimersByTime(250); });
      expect(screen.getByTestId('auto-approve-countdown')).toHaveTextContent('3s');

      act(() => { vi.advanceTimersByTime(1000); });
      expect(screen.getByTestId('auto-approve-countdown')).toHaveTextContent('2s');

      act(() => { vi.advanceTimersByTime(1000); });
      expect(screen.getByTestId('auto-approve-countdown')).toHaveTextContent('1s');
    });

    it('falls back to "starting…" once judgeStartsAt is in the past (before checking arrives)', () => {
      const judgeStartsAt = Date.now() + 500;
      render(
        <PermissionPrompt
          permission={permission}
          onReply={vi.fn()}
          autoApproveCapable
          autoApproveEnabled
          judgeStartsAt={judgeStartsAt}
        />,
      );

      act(() => { vi.advanceTimersByTime(250); });
      expect(screen.getByTestId('auto-approve-countdown')).toBeInTheDocument();

      act(() => { vi.advanceTimersByTime(500); });

      expect(screen.queryByTestId('auto-approve-countdown')).not.toBeInTheDocument();
      expect(screen.getByText(/starting/)).toBeInTheDocument();
    });

    it('shows spinner in footer when autoApproveChecking is true', () => {
      render(
        <PermissionPrompt
          permission={permission}
          onReply={vi.fn()}
          autoApproveCapable
          autoApproveEnabled
          autoApproveChecking
        />,
      );

      expect(screen.queryByTestId('auto-approve-countdown')).not.toBeInTheDocument();
      expect(screen.getByText(/running/)).toBeInTheDocument();
    });

    it('checking wins over countdown when both are set', () => {
      render(
        <PermissionPrompt
          permission={permission}
          onReply={vi.fn()}
          autoApproveCapable
          autoApproveEnabled
          autoApproveChecking
          judgeStartsAt={Date.now() + 5000}
        />,
      );

      expect(screen.queryByTestId('auto-approve-countdown')).not.toBeInTheDocument();
      expect(screen.getByText(/running/)).toBeInTheDocument();
    });

    it('shows "Enable auto-approve" when autoApproveEnabled is false', () => {
      render(
        <PermissionPrompt
          permission={permission}
          onReply={vi.fn()}
          autoApproveCapable
          autoApproveEnabled={false}
        />,
      );

      expect(screen.getByTestId('enable-auto-approve')).toBeInTheDocument();
      expect(screen.queryByText(/Auto-approve on/)).not.toBeInTheDocument();
    });

    it('action buttons are never disabled during countdown or checking', () => {
      const judgeStartsAt = Date.now() + 5000;
      render(
        <PermissionPrompt
          permission={permission}
          onReply={vi.fn()}
          autoApproveCapable
          autoApproveEnabled
          judgeStartsAt={judgeStartsAt}
        />,
      );

      const buttons = screen.getAllByRole('button', { name: /Allow|Reject/i });
      buttons.forEach((btn) => expect(btn).not.toBeDisabled());

      act(() => { vi.advanceTimersByTime(5100); });
      buttons.forEach((btn) => expect(btn).not.toBeDisabled());
    });

    it('countdown survives session switches (judgeStartsAt is absolute time)', () => {
      const judgeStartsAt = Date.now() + 2000;

      const { unmount } = render(
        <PermissionPrompt
          permission={permission}
          onReply={vi.fn()}
          autoApproveCapable
          autoApproveEnabled
          judgeStartsAt={judgeStartsAt}
        />,
      );

      act(() => { vi.advanceTimersByTime(250); });
      expect(screen.getByTestId('auto-approve-countdown')).toHaveTextContent('2s');

      act(() => { vi.advanceTimersByTime(1000); });
      unmount();

      render(
        <PermissionPrompt
          permission={permission}
          onReply={vi.fn()}
          autoApproveCapable
          autoApproveEnabled
          judgeStartsAt={judgeStartsAt}
        />,
      );

      // After remount, advance to trigger the first tick.
      act(() => { vi.advanceTimersByTime(250); });
      // Should show 1s remaining, not restart at 2s.
      expect(screen.getByTestId('auto-approve-countdown')).toHaveTextContent('1s');
    });
  });

  describe('judge verdict reasoning (flagged state)', () => {
    // When the AI judge returns "unsafe" the prompt stays for the user
    // and shows the one-line conclusion inline. The transient judge
    // session is deleted as soon as the verdict is parsed, so there is
    // no longer a magnifying-glass link to a full reasoning view —
    // judgeReasoning is the only artifact surfaced to the user.
    it('renders the one-line reasoning when judge flagged the permission', () => {
      render(
        <PermissionPrompt
          permission={permission}
          onReply={vi.fn()}
          autoApproveCapable
          autoApproveEnabled
          judgeReasoning="Touches files outside the working tree."
        />,
      );

      const reasoning = screen.getByTestId('judge-reasoning');
      expect(reasoning).toHaveTextContent('Touches files outside the working tree.');
      // The prompt shows the "unsafe" status alongside the reasoning.
      expect(screen.getByText(/Auto-approve on . unsafe/)).toBeInTheDocument();
    });

    it('does not render any "view judge session" button', () => {
      // Regression: the magnifying-glass link used to open the judge
      // session in a new tab. That session no longer exists by the
      // time the user sees the prompt, so the button has been removed.
      render(
        <PermissionPrompt
          permission={permission}
          onReply={vi.fn()}
          autoApproveCapable
          autoApproveEnabled
          judgeReasoning="Reasoning."
        />,
      );

      expect(screen.queryByRole('button', { name: /view.*reasoning/i })).not.toBeInTheDocument();
      expect(screen.queryByRole('button', { name: /view.*judge.*session/i })).not.toBeInTheDocument();
      expect(screen.queryByTestId('view-judge-session')).not.toBeInTheDocument();
    });

    it('falls back to the "starting…" footer when judgeReasoning is null', () => {
      // Without reasoning there is no surviving signal from the judge
      // to display — the prompt reverts to the "starting…" fallback
      // until the countdown / checking event arrives.
      render(
        <PermissionPrompt
          permission={permission}
          onReply={vi.fn()}
          autoApproveCapable
          autoApproveEnabled
          judgeReasoning={null}
        />,
      );

      expect(screen.queryByTestId('judge-reasoning')).not.toBeInTheDocument();
      expect(screen.queryByText(/Auto-approve on . unsafe/)).not.toBeInTheDocument();
      expect(screen.getByText(/Auto-approve on . starting/)).toBeInTheDocument();
    });
  });
});
