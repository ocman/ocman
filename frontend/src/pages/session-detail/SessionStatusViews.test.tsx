// @vitest-environment jsdom
import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { expect, it, vi } from 'vitest';
import { SessionEmptyDetail, SessionLoadError } from './SessionStatusViews';

it('announces session-read failures and retries from the keyboard', async () => {
  const user = userEvent.setup();
  const retry = vi.fn();
  render(<SessionLoadError message="Owner read failed" onRetry={retry} />);
  expect(screen.getByRole('alert')).toHaveTextContent('Owner read failed');
  await user.tab();
  await user.keyboard('{Enter}');
  expect(retry).toHaveBeenCalledOnce();
});

it('keeps the current shortcut and new-session command in the empty view', () => {
  render(<SessionEmptyDetail shortcutLabel="Alt+Space" />);
  expect(screen.getByTestId('empty-detail')).toHaveTextContent('No session open.');
  expect(screen.getByText('Alt+Space').tagName).toBe('KBD');
  expect(screen.getByText('/new').tagName).toBe('CODE');
});
