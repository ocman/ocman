// @vitest-environment jsdom
import { beforeEach, expect, it } from 'vitest';
import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { SnoozeSettings } from './SnoozeSettings';
import { useSnoozePreferences } from '../lib/snoozePreferences';
import { DEFAULT_SNOOZE_CHOICES } from '../lib/snooze';

beforeEach(() => {
  localStorage.clear();
  useSnoozePreferences.setState({ choices: DEFAULT_SNOOZE_CHOICES });
});

it('edits and persists duration and morning shortcuts, adds, removes and resets choices', async () => {
  const user = userEvent.setup();
  render(<SnoozeSettings />);
  fireEvent.change(screen.getByRole('spinbutton', { name: 'Quick choice 1 minutes' }), { target: { value: '15' } });
  const time = screen.getByLabelText('Quick choice 4 time');
  fireEvent.change(time, { target: { value: '07:30' } });
  fireEvent.blur(time);
  expect(useSnoozePreferences.getState().choices[0]).toEqual({ kind: 'duration', minutes: 15 });
  expect(useSnoozePreferences.getState().choices[3]).toEqual({ kind: 'tomorrow', time: '07:30' });
  expect(JSON.parse(localStorage.getItem('ocman-snooze-choices')!).state.choices).toEqual(useSnoozePreferences.getState().choices);
  await user.click(screen.getByRole('button', { name: 'Add quick choice' }));
  expect(useSnoozePreferences.getState().choices).toHaveLength(6);
  await user.click(screen.getByRole('button', { name: 'Remove quick choice 6' }));
  expect(useSnoozePreferences.getState().choices).toHaveLength(5);
  await user.click(screen.getByRole('button', { name: 'Reset defaults' }));
  expect(useSnoozePreferences.getState().choices).toEqual(DEFAULT_SNOOZE_CHOICES);
});

it('changes shortcut types and allows removing all quick choices', async () => {
  const user = userEvent.setup();
  useSnoozePreferences.setState({ choices: [{ kind: 'duration', minutes: 30 }] });
  render(<SnoozeSettings />);
  await user.click(screen.getByRole('combobox', { name: 'Quick choice 1 type' }));
  await user.click(screen.getByRole('option', { name: 'Tomorrow' }));
  await waitFor(() => expect(useSnoozePreferences.getState().choices).toEqual([{ kind: 'tomorrow', time: '08:00' }]));
  await user.click(screen.getByRole('combobox', { name: 'Quick choice 1 type' }));
  await user.click(screen.getByRole('option', { name: 'Next Monday' }));
  expect(useSnoozePreferences.getState().choices[0].kind).toBe('monday');
  await user.click(screen.getByRole('combobox', { name: 'Quick choice 1 type' }));
  await user.click(screen.getByRole('option', { name: 'After a duration' }));
  expect(useSnoozePreferences.getState().choices).toEqual([{ kind: 'duration', minutes: 60 }]);
  await user.click(screen.getByRole('button', { name: 'Remove quick choice 1' }));
  expect(useSnoozePreferences.getState().choices).toEqual([]);
});
