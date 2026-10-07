// @vitest-environment jsdom
import { expect, it, vi } from 'vitest';
import { waitFor } from '@testing-library/react';
import { startFirstSubmission, useFirstSubmission } from './firstSubmission';

it('shares the first-delivery lock with an independent tab before completion navigation', async () => {
  let finish!: () => void;
  const delivery = new Promise<void>((resolve) => { finish = resolve; });
  startFirstSubmission('peer-child', 'first message', () => delivery);
  vi.resetModules();
  const peer = await import('./firstSubmission');
  expect(peer.useFirstSubmission.getState().entries['peer-child']?.pending).toBe(true);
  finish();
  await waitFor(() => expect(useFirstSubmission.getState().entries['peer-child']).toBeUndefined());
  window.dispatchEvent(new StorageEvent('storage', { key: 'ocman.firstSubmission.v1:peer-child', newValue: null }));
  expect(peer.useFirstSubmission.getState().entries['peer-child']).toBeUndefined();
});
