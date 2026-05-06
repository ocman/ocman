// @vitest-environment jsdom

import { render, screen, waitFor } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import { PermissionPrompt, type PendingPermission } from './PermissionPrompt';

const permission: PendingPermission = {
  permissionId: 'perm-1',
  permission: 'Write to /tmp/output.txt',
  patterns: [],
};

describe('PermissionPrompt', () => {
  it('handles allow-once hotkey even when the dialog is not focused', async () => {
    const onReply = vi.fn();
    render(<PermissionPrompt permission={permission} onReply={onReply} />);

    screen.getByRole('dialog', { name: 'Permission required' });
    document.body.focus();
    window.dispatchEvent(new KeyboardEvent('keydown', { key: 'a', bubbles: true }));

    await waitFor(() => expect(onReply).toHaveBeenCalledWith('once'));
  });

  it('handles reject hotkey even when the dialog is not focused', async () => {
    const onReply = vi.fn();
    render(<PermissionPrompt permission={permission} onReply={onReply} />);

    screen.getByRole('dialog', { name: 'Permission required' });
    document.body.focus();
    window.dispatchEvent(new KeyboardEvent('keydown', { key: 'r', bubbles: true }));

    await waitFor(() => expect(onReply).toHaveBeenCalledWith('reject'));
  });
});
