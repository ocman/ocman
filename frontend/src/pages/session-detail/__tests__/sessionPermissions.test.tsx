// @vitest-environment jsdom
import { act, screen } from '@testing-library/react';
import { expect, it, vi } from 'vitest';
import { fullCaps, makeSession, makeSessionDetail, renderSessionPage } from './harness';
import { sessionPermissionPolicy } from '../../../lib/sessionPermissionPolicy';
import { useUiStore } from '../../../lib/uiStore';

vi.mock('../../../lib/sessionPermissionPolicy', () => ({ sessionPermissionPolicy: { get: vi.fn().mockResolvedValue({ rules: [], requests: [] }), save: vi.fn() } }));

it('opens Ocman permissions from the palette without native rules or a live composer', async () => {
  renderSessionPage({
    sessionId: 'sess_1',
    detail: makeSessionDetail(makeSession({ liveConnection: false })),
    caps: { ...fullCaps(), permissionRules: false, composer: false },
  });
  await screen.findByTestId('assistant-thread');
  act(() => useUiStore.getState().dispatchCommand({ kind: 'scoped', id: 'scoped.permissions', label: 'permissions', description: 'Session approval rules' }));
  await screen.findByRole('dialog', { name: 'Session permissions' });
  expect(sessionPermissionPolicy.get).toHaveBeenCalledWith('sess_1', 'opencode');
});
