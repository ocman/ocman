// @vitest-environment jsdom
import { createRef } from 'react';
import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { expect, it, vi } from 'vitest';
import { ToggleField } from './ToggleField';

it('preserves native keyboard selection, label, ref and form values', async () => {
  const user = userEvent.setup();
  const ref = createRef<HTMLInputElement>();
  const { container } = render(<form><ToggleField ref={ref} label="Notifications" name="notifications" value="enabled" defaultChecked /></form>);
  const toggle = screen.getByRole('checkbox', { name: 'Notifications' });
  expect(ref.current).toBe(toggle);
  expect(toggle).toBeChecked();
  expect(new FormData(container.querySelector('form')!).get('notifications')).toBe('enabled');
  await user.tab();
  expect(toggle).toHaveFocus();
  await user.keyboard(' ');
  expect(toggle).not.toBeChecked();
});

it('follows controlled values and blocks disabled interaction', async () => {
  const user = userEvent.setup();
  const onChange = vi.fn();
  const { rerender } = render(<ToggleField label="Notifications" checked={false} onChange={onChange} />);
  await user.click(screen.getByRole('checkbox'));
  expect(onChange).toHaveBeenCalledOnce();
  rerender(<ToggleField label="Notifications" checked disabled onChange={onChange} />);
  expect(screen.getByRole('checkbox')).toBeChecked();
  await user.click(screen.getByRole('checkbox'));
  expect(onChange).toHaveBeenCalledOnce();
  expect(screen.getByRole('checkbox')).toBeDisabled();
});
