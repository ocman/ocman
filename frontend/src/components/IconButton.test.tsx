// @vitest-environment jsdom
import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { expect, it, vi } from 'vitest';
import { IconButton } from './IconButton';
import { RefreshButton } from './RefreshButton';

it('names icon actions and supports keyboard activation without submitting the surrounding form', async () => {
  const user = userEvent.setup();
  const onClick = vi.fn();
  const onSubmit = vi.fn((event) => event.preventDefault());
  render(<form onSubmit={onSubmit}><IconButton icon="bi-arrows-fullscreen" label="Fullscreen" onClick={onClick} /></form>);
  const button = screen.getByRole('button', { name: 'Fullscreen' });
  expect(button).toHaveAttribute('title', 'Fullscreen');
  expect(button.querySelector('i')).toHaveAttribute('aria-hidden', 'true');
  await user.tab();
  expect(button).toHaveFocus();
  await user.keyboard('{Enter}');
  expect(onClick).toHaveBeenCalledOnce();
  expect(onSubmit).not.toHaveBeenCalled();
});

it('forwards native attributes and allows explicit styling, title, and submit behaviour', async () => {
  const user = userEvent.setup();
  const onSubmit = vi.fn((event) => event.preventDefault());
  render(<form onSubmit={onSubmit}><IconButton icon="bi-check" label="Save" title="Save changes" type="submit" size="normal" variant="accent" className="custom-action" aria-controls="editor" /></form>);
  const button = screen.getByRole('button', { name: 'Save' });
  expect(button).toHaveAttribute('title', 'Save changes');
  expect(button).toHaveAttribute('aria-controls', 'editor');
  expect(button).toHaveClass('oc-button', 'oc-icon-button', 'oc-button--accent', 'oc-button--normal', 'custom-action');
  await user.click(button);
  expect(onSubmit).toHaveBeenCalledOnce();
});

it('blocks repeat refreshes while busy and re-enables when the request settles', async () => {
  const user = userEvent.setup();
  const onClick = vi.fn();
  const { rerender } = render(<RefreshButton onClick={onClick} />);
  const button = screen.getByRole('button', { name: 'Refresh' });
  expect(button).toHaveAttribute('aria-busy', 'false');
  await user.click(button);
  expect(onClick).toHaveBeenCalledOnce();
  rerender(<RefreshButton onClick={onClick} loading />);
  expect(button).toBeDisabled();
  expect(button).toHaveAttribute('aria-busy', 'true');
  await user.click(button);
  expect(onClick).toHaveBeenCalledOnce();
  rerender(<RefreshButton onClick={onClick} />);
  await user.click(button);
  expect(onClick).toHaveBeenCalledTimes(2);
});

it.each([false, true])('keeps externally disabled refreshes disabled when loading=%s', async (loading) => {
  const user = userEvent.setup();
  const onClick = vi.fn();
  render(<RefreshButton label="Refresh sessions" title="Reload sessions" size="small" variant="default" disabled loading={loading} onClick={onClick} />);
  const button = screen.getByRole('button', { name: 'Refresh sessions' });
  expect(button).toBeDisabled();
  expect(button).toHaveAttribute('title', 'Reload sessions');
  expect(button).toHaveClass('oc-button--small', 'oc-button--default');
  await user.click(button);
  expect(onClick).not.toHaveBeenCalled();
});
