// @vitest-environment jsdom
import { render, screen, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { expect, it, vi } from 'vitest';
import { InlineAlert } from './InlineAlert';

it('announces a message without inventing an action', () => {
  render(<InlineAlert>Could not load <strong>logs</strong>.</InlineAlert>);
  const alert = screen.getByRole('alert');
  expect(alert).toHaveTextContent('Could not load logs.');
  expect(within(alert).queryByRole('button')).not.toBeInTheDocument();
});

it('retries from the keyboard without submitting a surrounding form', async () => {
  const user = userEvent.setup();
  const retry = vi.fn();
  const submit = vi.fn((event) => event.preventDefault());
  render(<form onSubmit={submit}><InlineAlert onRetry={retry}>Request failed.</InlineAlert></form>);
  const button = within(screen.getByRole('alert')).getByRole('button', { name: 'Retry' });
  await user.tab();
  expect(button).toHaveFocus();
  await user.keyboard('{Enter}');
  expect(retry).toHaveBeenCalledOnce();
  expect(submit).not.toHaveBeenCalled();
});

it('blocks duplicate retries while busy and re-enables after the parent settles', async () => {
  const user = userEvent.setup();
  const retry = vi.fn();
  const { rerender } = render(<InlineAlert onRetry={retry} retrying>Request failed.</InlineAlert>);
  const button = screen.getByRole('button', { name: 'Retry' });
  expect(button).toBeDisabled();
  expect(button).toHaveAttribute('aria-busy', 'true');
  await user.click(button);
  expect(retry).not.toHaveBeenCalled();
  rerender(<InlineAlert onRetry={retry}>Request failed again.</InlineAlert>);
  expect(button).toBeEnabled();
  expect(button).toHaveAttribute('aria-busy', 'false');
  await user.click(button);
  expect(retry).toHaveBeenCalledOnce();
});
