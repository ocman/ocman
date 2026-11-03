// @vitest-environment jsdom
import { describe, expect, it, vi } from 'vitest';
import { act, fireEvent, render, screen } from '@testing-library/react';
import * as Toast from '@radix-ui/react-toast';
import { SessionToasts, type SessionToastsProps } from './SessionToasts';

function renderToasts(over: Partial<SessionToastsProps> = {}) {
  const props: SessionToastsProps = {
    showRenameToast: false, setShowRenameToast: vi.fn(),
    restartToastMessage: null, setRestartToastMessage: vi.fn(),
    showCreateSessionErrorToast: false, setShowCreateSessionErrorToast: vi.fn(),
    showDisconnectedToast: true, setShowDisconnectedToast: vi.fn(),
    copyToastMessage: null, setCopyToastMessage: vi.fn(),
    sendRetryDelaySeconds: null, setSendRetryDelaySeconds: vi.fn(),
    tmuxAvailable: true, liveConnectionHint: true, hasDirectory: true,
    platformName: 'OpenCode', launching: false, onLaunch: vi.fn(),
    ...over,
  };
  render(<Toast.Provider><SessionToasts {...props} /></Toast.Provider>);
  return props;
}

describe('SessionToasts disconnected toast', () => {
  it('names the session platform and launches it', () => {
    const props = renderToasts();
    expect(screen.getByText('OpenCode is not running for this session.')).toBeInTheDocument();
    fireEvent.click(screen.getByRole('button', { name: 'Launch OpenCode' }));
    expect(props.setShowDisconnectedToast).toHaveBeenCalledWith(false);
    expect(props.onLaunch).toHaveBeenCalledOnce();
  });

  it('clears each transient toast when it times out', () => {
    vi.useFakeTimers();
    try {
      const props = renderToasts({
        showDisconnectedToast: false, restartToastMessage: 'Restart failed', copyToastMessage: 'Copied', sendRetryDelaySeconds: 3,
      });
      expect(screen.getByText('Restart failed')).toBeInTheDocument();
      expect(screen.getByTestId('send-retry-toast')).toHaveTextContent('Retrying in 3s');
      act(() => { vi.advanceTimersByTime(61_000); });
      expect(props.setRestartToastMessage).toHaveBeenCalledWith(null);
      expect(props.setCopyToastMessage).toHaveBeenCalledWith(null);
      expect(props.setSendRetryDelaySeconds).toHaveBeenCalledWith(null);
    } finally {
      vi.useRealTimers();
    }
  });

  it('falls back to a generic agent name and disables launch while launching', () => {
    renderToasts({ platformName: '', launching: true });
    expect(screen.getByText('The agent is not running for this session.')).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Launching…' })).toBeDisabled();
  });
});
