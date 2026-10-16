// @vitest-environment jsdom
import { act, cleanup, fireEvent, render, screen } from '@testing-library/react';
import * as Toast from '@radix-ui/react-toast';
import { afterEach, expect, it, vi } from 'vitest';
import { SessionWarningBanner } from './SessionWarningBanner';

afterEach(() => { cleanup(); vi.useRealTimers(); });

it('keeps an external-owner warning visible until dismissed', () => {
  vi.useFakeTimers();
  const onDismiss = vi.fn();
  render(<Toast.Provider>
    <SessionWarningBanner warning={{ kind: 'external-session-owner', message: 'Both instances can modify the session.' }} onDismiss={onDismiss} />
    <Toast.Viewport />
  </Toast.Provider>);
  act(() => { vi.advanceTimersByTime(60_000); });
  expect(screen.getByTestId('session-warning-external-session-owner')).toBeVisible();
  expect(screen.getByText('Also open in another Claude instance')).toBeVisible();
  expect(onDismiss).not.toHaveBeenCalled();
  fireEvent.click(screen.getByRole('button', { name: 'Dismiss session warning' }));
  expect(onDismiss).toHaveBeenCalledOnce();
});

it('retains the timeout for other session warnings', () => {
  vi.useFakeTimers();
  const onDismiss = vi.fn();
  render(<Toast.Provider>
    <SessionWarningBanner warning={{ kind: 'other', message: 'An advisory warning.' }} onDismiss={onDismiss} />
    <Toast.Viewport />
  </Toast.Provider>);
  act(() => { vi.advanceTimersByTime(10_001); });
  expect(onDismiss).toHaveBeenCalledOnce();
});
