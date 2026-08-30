// @vitest-environment jsdom
import { createRef } from 'react';
import { act, fireEvent, render, screen } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { Composer, type ComposerHandle } from './Composer';
import { BackendUnavailableError } from '../../lib/api';

afterEach(() => {
  vi.useRealTimers();
  vi.unstubAllGlobals();
});

describe('Composer queue', () => {
  it('renders queued shell commands in the follow-up queue', () => {
    const onCancelQueuedShell = vi.fn();
    render(<Composer isRunning queuedShellCommand="git status" onCancelQueuedShell={onCancelQueuedShell} />);

    expect(screen.getByRole('list', { name: 'Queued follow-up messages' })).toHaveTextContent('!git status');
    expect(screen.queryByTestId('shell-queued')).not.toBeInTheDocument();
    fireEvent.click(screen.getByLabelText('Cancel queued shell command'));
    expect(onCancelQueuedShell).toHaveBeenCalledOnce();
  });

  it('shows active duration and hides the shortcut hint while running', () => {
    vi.useFakeTimers();
    vi.setSystemTime(new Date('2026-01-01T00:00:00Z'));
    const { rerender } = render(
      <Composer isRunning={false} contextTokens={12_000} activeDurationMs={90_000} />,
    );

    expect(screen.getByText('1m 30s')).toBeInTheDocument();
    expect(screen.getByRole('button', { name: /for shortcuts/i })).toBeInTheDocument();

    rerender(<Composer isRunning contextTokens={12_000} activeDurationMs={90_000} />);

    expect(screen.getByText('1m 30s')).toBeInTheDocument();
    expect(screen.queryByRole('button', { name: /for shortcuts/i })).not.toBeInTheDocument();

    act(() => vi.advanceTimersByTime(1_000));

    expect(screen.getByText('1m 31s')).toBeInTheDocument();
  });
});

describe('Composer input', () => {
  it.each([
    ['touch-only', true, false, false],
    ['fine-pointer', false, true, true],
    ['hybrid', true, true, true],
    ['unknown pointer', undefined, undefined, true],
  ])('%s device focus after switching sessions: %s', async (_device, coarse, fine, expectedFocus) => {
    if (coarse !== undefined) {
      vi.stubGlobal('matchMedia', vi.fn((query: string) => ({
        matches: query === '(any-pointer: coarse)' ? coarse : fine,
      })));
    }
    const { rerender } = render(<Composer key="session-a" sessionId="session-a" isRunning={false} />);

    rerender(<Composer key="session-b" sessionId="session-b" isRunning={false} />);
    await act(async () => { await new Promise((resolve) => setTimeout(resolve, 1)); });

    expect(document.activeElement === screen.getByRole('textbox')).toBe(expectedFocus);
  });

  it('updates slash and bash state without CustomEvents', () => {
    Element.prototype.scrollIntoView = vi.fn();
    const dispatch = vi.spyOn(HTMLTextAreaElement.prototype, 'dispatchEvent');
    render(<Composer isRunning={false} shellExec />);
    const input = screen.getByRole('textbox');

    fireEvent.input(input, { target: { value: '/he' } });
    expect(screen.getByText('/help')).toBeInTheDocument();

    fireEvent.input(input, { target: { value: '!ls' } });
    expect(screen.getByText('shell')).toBeInTheDocument();
    expect(dispatch.mock.calls.some(([event]) => event instanceof CustomEvent)).toBe(false);
  });

  it('opens model and agent pickers through the typed composer handle', () => {
    const composerRef = createRef<ComposerHandle>();
    render(
      <Composer
        isRunning={false}
        composerRef={composerRef}
        models={['anthropic/claude']}
        agents={[{ name: 'build', mode: 'primary' }]}
      />,
    );

    act(() => composerRef.current?.openModelPicker('cla'));
    expect(screen.getByRole('combobox')).toHaveValue('cla');
    fireEvent.keyDown(screen.getByRole('combobox'), { key: 'Escape' });

    act(() => composerRef.current?.openAgentPicker('bui'));
    expect(screen.getByRole('combobox')).toHaveValue('bui');
  });

  it('adds pasted images directly without CustomEvents', async () => {
    const dispatch = vi.spyOn(HTMLTextAreaElement.prototype, 'dispatchEvent');
    const image = new File(['image'], 'image.png', { type: 'image/png' });
    render(<Composer isRunning={false} />);

    fireEvent.paste(screen.getByRole('textbox'), {
      clipboardData: { items: [{ type: image.type, getAsFile: () => image }] },
    });

    expect(await screen.findByAltText('Attachment 1')).toBeInTheDocument();
    expect(dispatch.mock.calls.some(([event]) => event instanceof CustomEvent)).toBe(false);
  });

  it('does not rewrite its height while typing', () => {
    render(<Composer isRunning={false} />);
    const input = screen.getByRole('textbox');

    fireEvent.input(input, { target: { value: 'hello' } });

    expect((input as HTMLTextAreaElement).style.height).toBe('');
  });

  // Enter sends now (mid-turn included); Ctrl/Cmd+Enter is the explicit
  // "hold this for the next idle edge" gesture (#58).
  it.each([
    ['Enter', {}, false],
    ['Ctrl+Enter', { ctrlKey: true }, true],
    ['Cmd+Enter', { metaKey: true }, true],
  ])('routes %s to onSend with queue=%o', async (_label, modifiers, expected) => {
    const onSend = vi.fn().mockResolvedValue(undefined);
    render(<Composer isRunning onSend={onSend} />);
    const input = screen.getByRole('textbox');

    fireEvent.input(input, { target: { value: 'follow up' } });
    fireEvent.keyDown(input, { key: 'Enter', ...modifiers });
    await act(async () => {});

    expect(onSend).toHaveBeenCalledWith('follow up', undefined, expected);
  });

  // The textarea is disabled while sending, and a real browser blurs a
  // focused control when it becomes disabled — re-enabling does not put
  // focus back, so the caret was lost after every Enter.
  //
  // jsdom implements neither half of that: it does not blur on disable,
  // and `.blur()` on an already-disabled element is a no-op. So the drop
  // to <body> is staged here by focusing a throwaway element and removing
  // it. The faithful version of this lives in e2e/composer.spec.ts, in a
  // real browser; this one pins the restore logic cheaply.
  const dropFocusToBody = () => {
    const sacrifice = document.createElement('input');
    document.body.appendChild(sacrifice);
    sacrifice.focus();
    sacrifice.remove();
  };

  it('returns focus to the textarea after a send completes', async () => {
    const onSend = vi.fn().mockResolvedValue(undefined);
    render(<Composer isRunning onSend={onSend} />);
    const input = screen.getByRole('textbox') as HTMLTextAreaElement;

    // Let the mount auto-focus settle first. It runs on a setTimeout(0)
    // and re-focuses the composer whenever the active element is not a
    // text field — if it is still pending it lands after the drop below
    // and hides whether the send restored focus or not.
    await act(async () => { await new Promise((r) => setTimeout(r, 1)); });

    input.focus();
    expect(document.activeElement).toBe(input);

    fireEvent.input(input, { target: { value: 'hello' } });
    fireEvent.keyDown(input, { key: 'Enter' });
    dropFocusToBody(); // what the browser does when `disabled` flips on
    expect(document.activeElement).toBe(document.body);
    await act(async () => {});

    expect(document.activeElement).toBe(input);
  });

  it('leaves focus alone when the user moved it during the send', async () => {
    const onSend = vi.fn().mockResolvedValue(undefined);
    render(<Composer isRunning onSend={onSend} />);
    const input = screen.getByRole('textbox') as HTMLTextAreaElement;
    const outside = document.createElement('button');
    document.body.appendChild(outside);

    await act(async () => { await new Promise((r) => setTimeout(r, 1)); });

    input.focus();
    fireEvent.input(input, { target: { value: 'hello' } });
    fireEvent.keyDown(input, { key: 'Enter' });
    outside.focus(); // user clicks away mid-send
    await act(async () => {});

    expect(document.activeElement).toBe(outside);
    outside.remove();
  });

  it('does not steal focus when the send was started from elsewhere', async () => {
    const onSend = vi.fn().mockResolvedValue(undefined);
    render(<Composer isRunning onSend={onSend} />);
    const input = screen.getByRole('textbox') as HTMLTextAreaElement;
    const outside = document.createElement('button');
    document.body.appendChild(outside);

    // Let the mount auto-focus settle first, otherwise its setTimeout(0)
    // lands mid-test and moves focus for reasons unrelated to sending.
    await act(async () => { await new Promise((r) => setTimeout(r, 1)); });

    fireEvent.input(input, { target: { value: 'hello' } });
    outside.focus();
    expect(document.activeElement).toBe(outside);

    fireEvent.keyDown(input, { key: 'Enter' });
    await act(async () => {});

    expect(document.activeElement).toBe(outside);
    outside.remove();
  });

  // #459: with the backend down the retry loop must be bounded (with
  // backoff), then give up — unlocking the composer with the draft
  // intact so the user can edit, copy, or retry deliberately. The old
  // loop retried every second forever and kept the composer locked.
  it('gives up after bounded backoff and unlocks with the draft intact', async () => {
    vi.useFakeTimers();
    const onSend = vi.fn().mockRejectedValue(new BackendUnavailableError());
    render(<Composer isRunning={false} onSend={onSend} />);
    const input = screen.getByRole('textbox');

    fireEvent.input(input, { target: { value: 'do not lose me' } });
    fireEvent.keyDown(input, { key: 'Enter' });
    await act(async () => {});
    expect(onSend).toHaveBeenCalledTimes(1);

    // Exhaust the backoff schedule (1s, 2s, 4s, 8s, 16s) plus slack.
    for (let i = 0; i < 10; i++) {
      await act(async () => { vi.advanceTimersByTime(20_000); });
    }

    // Bounded: initial attempt + 5 retries, then no more ticks fire.
    expect(onSend).toHaveBeenCalledTimes(6);
    await act(async () => { vi.advanceTimersByTime(60_000); });
    expect(onSend).toHaveBeenCalledTimes(6);

    // Unlocked, draft preserved.
    expect(input).toHaveValue('do not lose me');
    expect(input).not.toBeDisabled();
  });

  it('keeps the draft locked and retries while the backend is unavailable', async () => {
    vi.useFakeTimers();
    const onRetryChange = vi.fn();
    const onSend = vi.fn()
      .mockRejectedValueOnce(new BackendUnavailableError())
      .mockResolvedValueOnce(undefined);
    render(<Composer isRunning={false} onSend={onSend} onRetryChange={onRetryChange} />);
    const input = screen.getByRole('textbox');

    fireEvent.input(input, { target: { value: 'keep this message' } });
    fireEvent.keyDown(input, { key: 'Enter' });

    await act(async () => {});
    expect(input).toHaveValue('keep this message');
    expect(input).toBeDisabled();
    expect(screen.getByRole('button', { name: 'Sending message' })).toBeDisabled();
    expect(onRetryChange).toHaveBeenCalledWith(1);

    await act(async () => { vi.advanceTimersByTime(1_000); });
    expect(onSend).toHaveBeenCalledTimes(2);
    expect(onRetryChange).toHaveBeenLastCalledWith(null);
    expect(input).toHaveValue('');
    expect(input).not.toBeDisabled();
  });
});
