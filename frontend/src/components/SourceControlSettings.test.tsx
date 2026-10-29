// @vitest-environment jsdom
import { act, fireEvent, render, screen, waitFor } from '@testing-library/react';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { MemoryRouter } from 'react-router-dom';
import { ConfirmContext } from '../lib/useConfirm';
import { SourceControlSettings } from './SourceControlSettings';
import { prWorkflowApi } from '../lib/prWorkflowApi';

vi.mock('../lib/prWorkflowApi', () => ({ prWorkflowApi: { connections: vi.fn(), recheck: vi.fn(), saveCredential: vi.fn() } }));
const confirm = vi.fn();
const row = { host: 'github.com', provider: 'github' as const, source: 'GitHub CLI', status: 'authenticated', account: 'dries', help: 'Requests run on the hub.' };
function mount() { return render(<MemoryRouter><ConfirmContext.Provider value={confirm}><SourceControlSettings /></ConfirmContext.Provider></MemoryRouter>); }
beforeEach(() => { vi.resetAllMocks(); vi.mocked(prWorkflowApi.connections).mockResolvedValue({ connections: [row] }); confirm.mockResolvedValue(true); vi.mocked(prWorkflowApi.saveCredential).mockResolvedValue(); vi.mocked(prWorkflowApi.recheck).mockResolvedValue(); });

describe('SourceControlSettings', () => {
  it('shows the account and source and rechecks credentials', async () => {
    mount(); expect(await screen.findByText('authenticated as dries · GitHub CLI')).toBeInTheDocument();
    fireEvent.click(screen.getByRole('button', { name: 'Recheck credentials' }));
    expect(await screen.findByText('Credentials rechecked.')).toBeInTheDocument(); expect(prWorkflowApi.recheck).toHaveBeenCalled();
  });
  it('verifies and saves per-host tokens, clears secrets, and supports cancel', async () => {
    mount(); await screen.findByText('github.com');
    fireEvent.change(screen.getByLabelText('Forge provider'), { target: { value: 'forgejo' } });
    fireEvent.change(screen.getByLabelText('Forge host'), { target: { value: 'code.example.com' } });
    fireEvent.change(screen.getByLabelText('Forge access token'), { target: { value: 'secret' } });
    fireEvent.click(screen.getByRole('button', { name: 'Verify and save token' }));
    await waitFor(() => expect(prWorkflowApi.saveCredential).toHaveBeenCalledWith('code.example.com', 'forgejo', 'secret'));
    await waitFor(() => expect(screen.getByLabelText('Forge access token')).toHaveValue(''));
    fireEvent.change(screen.getByLabelText('Forge provider'), { target: { value: 'github' } });
    expect(screen.getByLabelText('Forge host')).toHaveValue('github.com');
    fireEvent.change(screen.getByLabelText('Forge access token'), { target: { value: 'draft' } }); fireEvent.click(screen.getByRole('button', { name: 'Cancel' })); expect(screen.getByLabelText('Forge access token')).toHaveValue('');
  });
  it('confirms removing a saved override', async () => {
    vi.mocked(prWorkflowApi.connections).mockResolvedValue({ connections: [{ ...row, source: 'saved token' }] });
    mount(); fireEvent.click(await screen.findByRole('button', { name: 'Remove token' }));
    await waitFor(() => expect(prWorkflowApi.saveCredential).toHaveBeenCalledWith('github.com', 'github', ''));
    confirm.mockResolvedValue(false); vi.mocked(prWorkflowApi.saveCredential).mockClear(); fireEvent.click(screen.getByRole('button', { name: 'Remove token' }));
    await act(async () => {}); expect(prWorkflowApi.saveCredential).not.toHaveBeenCalled();
  });
  it('distinguishes errors from empty state and offers retry', async () => {
    vi.mocked(prWorkflowApi.connections).mockRejectedValueOnce(new Error('offline')).mockResolvedValue({ connections: [] });
    mount(); expect(await screen.findByRole('alert')).toHaveTextContent('Could not load'); expect(screen.queryByText(/No forge credentials/)).not.toBeInTheDocument();
    fireEvent.click(screen.getByRole('button', { name: 'Retry' })); expect(await screen.findByText(/No forge credentials/)).toBeInTheDocument();
    vi.mocked(prWorkflowApi.recheck).mockRejectedValue(new Error('network unavailable')); fireEvent.click(screen.getByRole('button', { name: 'Recheck credentials' })); expect(await screen.findByRole('alert')).toHaveTextContent('network unavailable');
  });
});
