// @vitest-environment jsdom
import { act, fireEvent, render, screen } from '@testing-library/react';
import { afterEach, expect, it, vi } from 'vitest';
import { copyTextToClipboard } from '../lib/clipboard';
import { CopyButton } from './CopyButton';

vi.mock('../lib/clipboard', () => ({ copyTextToClipboard: vi.fn() }));
afterEach(() => { vi.resetAllMocks(); vi.useRealTimers(); });

it('blocks repeat copies while pending and clears successful feedback after two seconds', async () => {
  vi.useFakeTimers();
  let finish!: (ok: boolean) => void;
  vi.mocked(copyTextToClipboard).mockReturnValue(new Promise((resolve) => { finish = resolve; }));
  const submit = vi.fn((event) => event.preventDefault());
  render(<form onSubmit={submit}><CopyButton text="https://example.test/inbox" label="Copy URL" /></form>);
  const button = screen.getByRole('button', { name: 'Copy URL' });
  fireEvent.click(button);
  expect(button).toBeDisabled();
  expect(button).toHaveAttribute('aria-busy', 'true');
  expect(screen.getByRole('status')).toBeEmptyDOMElement();
  fireEvent.click(button);
  expect(copyTextToClipboard).toHaveBeenCalledExactlyOnceWith('https://example.test/inbox');
  expect(submit).not.toHaveBeenCalled();
  await act(async () => finish(true));
  expect(button).toHaveTextContent('Copied!');
  expect(screen.getByRole('status')).toHaveTextContent('Copied!');
  expect(button).toBeEnabled();
  act(() => vi.advanceTimersByTime(2000));
  expect(button).toHaveTextContent('Copy URL');
  expect(screen.getByRole('status')).toBeEmptyDOMElement();
});

it('reports failed copies and allows retrying without claiming success', async () => {
  vi.mocked(copyTextToClipboard).mockResolvedValueOnce(false).mockResolvedValueOnce(true);
  const { rerender } = render(<CopyButton text="code" />);
  const button = screen.getByRole('button', { name: 'Copy' });
  await act(async () => fireEvent.click(button));
  expect(screen.getByRole('status')).toHaveTextContent('Could not copy to clipboard. Try again.');
  expect(button).toHaveTextContent('Copy failed');
  expect(button).toHaveAttribute('title', 'Could not copy to clipboard. Try again.');
  await act(async () => fireEvent.click(button));
  expect(button).toHaveTextContent('Copied!');
  rerender(<CopyButton text="code" disabled />);
  fireEvent.click(button);
  expect(copyTextToClipboard).toHaveBeenCalledTimes(2);
});

it('does not let an older copy overwrite feedback for newer content', async () => {
  let first!: (ok: boolean) => void;
  let second!: (ok: boolean) => void;
  vi.mocked(copyTextToClipboard)
    .mockReturnValueOnce(new Promise((resolve) => { first = resolve; }))
    .mockReturnValueOnce(new Promise((resolve) => { second = resolve; }));
  const { rerender } = render(<CopyButton text="old" iconOnly />);
  const button = screen.getByRole('button', { name: 'Copy' });
  fireEvent.click(button);
  rerender(<CopyButton text="new" iconOnly className="code-copy" size="compact" />);
  expect(button).toBeEnabled();
  expect(button).toHaveClass('oc-icon-button', 'code-copy', 'oc-button--compact');
  fireEvent.click(button);
  await act(async () => second(true));
  await act(async () => first(false));
  expect(screen.getByRole('status')).toHaveTextContent('Copied!');
  expect(copyTextToClipboard).toHaveBeenLastCalledWith('new');
});

it.each([false, true])('cleans up on unmount with a completed copy=%s', async (completed) => {
  vi.useFakeTimers();
  let finish!: (ok: boolean) => void;
  vi.mocked(copyTextToClipboard).mockReturnValue(new Promise((resolve) => { finish = resolve; }));
  const { unmount } = render(<CopyButton text="code" />);
  fireEvent.click(screen.getByRole('button', { name: 'Copy' }));
  if (completed) await act(async () => finish(true));
  unmount();
  if (!completed) await act(async () => finish(true));
  expect(vi.getTimerCount()).toBe(0);
});
