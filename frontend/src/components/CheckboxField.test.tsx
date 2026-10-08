// @vitest-environment jsdom
import { expect, it, vi } from 'vitest';
import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { createRef } from 'react';
import { CheckboxField } from './CheckboxField';

it('associates the label and preserves native form submission and keyboard behavior', async () => {
  const user = userEvent.setup();
  const ref = createRef<HTMLInputElement>();
  const { container } = render(<form><CheckboxField ref={ref} label="Enabled" name="enabled" value="yes" required /></form>);
  const checkbox = screen.getByRole('checkbox', { name: 'Enabled' });
  expect(ref.current).toBe(checkbox);
  expect(checkbox).toBeRequired();
  await user.click(screen.getByText('Enabled'));
  expect(checkbox).toBeChecked();
  expect(new FormData(container.querySelector('form')!).get('enabled')).toBe('yes');
  await user.keyboard(' ');
  expect(checkbox).not.toBeChecked();
});

it('forwards checked state and change events without making disabled labels interactive', async () => {
  const user = userEvent.setup();
  const onChange = vi.fn();
  const { rerender } = render(<CheckboxField label="Enabled" checked onChange={onChange} />);
  await user.click(screen.getByRole('checkbox'));
  expect(onChange).toHaveBeenCalledOnce();
  expect(screen.getByRole('checkbox')).toBeChecked();
  rerender(<CheckboxField label="Enabled" checked disabled onChange={onChange} />);
  await user.click(screen.getByText('Enabled'));
  expect(onChange).toHaveBeenCalledOnce();
  expect(screen.getByRole('checkbox')).toBeDisabled();
});
