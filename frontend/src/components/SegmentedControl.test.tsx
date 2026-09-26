// @vitest-environment jsdom
import { useState } from 'react';
import { render, screen, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { expect, it, vi } from 'vitest';
import { SegmentedControl } from './SegmentedControl';
import { TimeRangeControl } from './TimeRangeControl';

it('changes numeric values with arrow keys and does not deselect the active option', async () => {
  const user = userEvent.setup();
  const onChange = vi.fn();
  function Filter() {
    const [value, setValue] = useState(168);
    return <TimeRangeControl value={value} onChange={(next) => { onChange(next); setValue(next); }} />;
  }
  render(<Filter />);
  const group = screen.getByRole('radiogroup', { name: 'Time range' });
  const selected = within(group).getByRole('radio', { name: '7d' });
  await user.tab();
  expect(selected).toHaveFocus();
  await user.keyboard('{ArrowRight}');
  expect(within(group).getByRole('radio', { name: '30d' })).toBeChecked();
  expect(onChange).toHaveBeenLastCalledWith(720);
  await user.keyboard('{ArrowRight}');
  const all = within(group).getByRole('radio', { name: 'All' });
  expect(all).toBeChecked();
  expect(onChange).toHaveBeenLastCalledWith(0);
  onChange.mockClear();
  await user.click(all);
  expect(all).toBeChecked();
  expect(onChange).not.toHaveBeenCalled();
});

it('keeps multiple groups independent and follows externally controlled values', async () => {
  const user = userEvent.setup();
  const options = [{ value: 'all', label: 'All' }, { value: 'unread', label: 'Unread' }];
  const onChange = vi.fn();
  const view = (value: string) => <>
    <SegmentedControl label="First" options={options} value={value} onChange={onChange} />
    <SegmentedControl label="Second" options={options} value="all" onChange={onChange} />
  </>;
  const { rerender } = render(view('all'));
  const first = within(screen.getByRole('radiogroup', { name: 'First' }));
  const second = within(screen.getByRole('radiogroup', { name: 'Second' }));
  expect(first.getByRole('radio', { name: 'All' }).getAttribute('name')).not.toBe(second.getByRole('radio', { name: 'All' }).getAttribute('name'));
  await user.click(first.getByRole('radio', { name: 'Unread' }));
  expect(onChange).toHaveBeenCalledWith('unread');
  rerender(view('unread'));
  expect(first.getByRole('radio', { name: 'Unread' })).toBeChecked();
  expect(second.getByRole('radio', { name: 'All' })).toBeChecked();
});

it('keeps compact icon choices named and leaves text-only choices visible', async () => {
  const user = userEvent.setup();
  function Filter() {
    const [value, setValue] = useState('all');
    return <SegmentedControl label="Messages" compact options={[
      { value: 'all', label: 'All', icon: 'bi-inbox' },
      { value: 'unread', label: 'Unread', icon: 'bi-envelope' },
      { value: 'archived', label: 'Archived' },
    ]} value={value} onChange={setValue} />;
  }
  render(<Filter />);
  expect(screen.getByText('All')).toBeVisible();
  expect(screen.getByText('Archived')).toBeVisible();
  expect(screen.queryByText('Unread')).not.toBeInTheDocument();
  const unread = screen.getByRole('radio', { name: 'Unread' });
  expect(unread).toHaveAttribute('title', 'Unread');
  expect(unread.closest('label')?.querySelector('i')).toHaveAttribute('aria-hidden', 'true');
  await user.click(unread);
  expect(screen.getByText('Unread')).toBeVisible();
  expect(screen.queryByText('All')).not.toBeInTheDocument();
});
