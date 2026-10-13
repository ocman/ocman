// @vitest-environment jsdom
import { act, fireEvent, render, screen, waitFor } from '@testing-library/react';
import { afterEach, beforeEach, expect, it, vi } from 'vitest';
import { NotificationsSection } from './SettingsSections';
import { useUiStore } from '../../lib/uiStore';
import { enablePush, disablePush, pushSubscribed, pushSupported } from '../../lib/pushNotifications';

vi.mock('../../lib/pushNotifications', () => ({ enablePush: vi.fn(), disablePush: vi.fn(), pushSubscribed: vi.fn(), pushSupported: vi.fn() }));

beforeEach(() => {
  vi.stubGlobal('Notification', { permission: 'granted', requestPermission: vi.fn().mockResolvedValue('granted') });
  vi.mocked(pushSupported).mockReturnValue(true);
  vi.mocked(pushSubscribed).mockResolvedValue(false);
  vi.mocked(enablePush).mockResolvedValue(undefined);
  vi.mocked(disablePush).mockResolvedValue(undefined);
  useUiStore.setState({ notificationsEnabled: false });
});
afterEach(() => { vi.unstubAllGlobals(); vi.resetAllMocks(); useUiStore.setState({ notificationsEnabled: false }); });

it('registers background push before marking notifications enabled', async () => {
  render(<NotificationsSection />);
  fireEvent.click(screen.getByLabelText('System notifications'));
  await waitFor(() => expect(enablePush).toHaveBeenCalledOnce());
  await waitFor(() => expect(useUiStore.getState().notificationsEnabled).toBe(true));
});

it('keeps notifications off and shows registration errors', async () => {
  vi.mocked(enablePush).mockRejectedValue(new Error('Push service unavailable'));
  render(<NotificationsSection />);
  fireEvent.click(screen.getByLabelText('System notifications'));
  expect(await screen.findByText('Push service unavailable')).toBeInTheDocument();
  expect(useUiStore.getState().notificationsEnabled).toBe(false);
});

it('offers existing notification users a background-push upgrade', async () => {
  useUiStore.setState({ notificationsEnabled: true });
  render(<NotificationsSection />);
  fireEvent.click(await screen.findByRole('button', { name: 'Enable background alerts' }));
  await waitFor(() => expect(enablePush).toHaveBeenCalledOnce());
  await waitFor(() => expect(screen.queryByRole('button', { name: 'Enable background alerts' })).not.toBeInTheDocument());
});

it('does not mark disabled when device removal fails', async () => {
  useUiStore.setState({ notificationsEnabled: true });
  vi.mocked(disablePush).mockRejectedValue(new Error('Could not remove device'));
  render(<NotificationsSection />);
  fireEvent.click(screen.getByLabelText('System notifications'));
  expect(await screen.findByText('Could not remove device')).toBeInTheDocument();
  expect(useUiStore.getState().notificationsEnabled).toBe(true);
});

it('unsubscribes when notifications are turned off', async () => {
  useUiStore.setState({ notificationsEnabled: true });
  render(<NotificationsSection />);
  fireEvent.click(screen.getByLabelText('System notifications'));
  await waitFor(() => expect(disablePush).toHaveBeenCalledOnce());
  await waitFor(() => expect(useUiStore.getState().notificationsEnabled).toBe(false));
});

it('requests permission on user interaction before subscribing', async () => {
  Object.assign(Notification, { permission: 'default' });
  render(<NotificationsSection />);
  expect(Notification.requestPermission).not.toHaveBeenCalled();
  fireEvent.click(screen.getByLabelText('System notifications'));
  await waitFor(() => expect(Notification.requestPermission).toHaveBeenCalledOnce());
  await waitFor(() => expect(enablePush).toHaveBeenCalledOnce());
});

it('shows Home Screen instructions when the browser lacks notifications', async () => {
  vi.stubGlobal('Notification', undefined);
  render(<NotificationsSection />);
  expect(screen.getByLabelText('System notifications')).toBeDisabled();
  expect(screen.getByText(/add ocman to your Home Screen/)).toBeInTheDocument();
  await act(async () => {});
});
