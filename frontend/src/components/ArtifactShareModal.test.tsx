// @vitest-environment jsdom
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { artifactsApi, type Artifact } from '../lib/artifactsApi';
import { copyTextToClipboard } from '../lib/clipboard';
import { ArtifactShareModal } from './ArtifactShareModal';

vi.mock('../lib/artifactsApi', async (orig) => ({
  ...(await orig<typeof import('../lib/artifactsApi')>()),
  artifactsApi: { shares: vi.fn(), share: vi.fn(), revokeShare: vi.fn() },
}));
vi.mock('../lib/clipboard', () => ({ copyTextToClipboard: vi.fn(async () => true) }));

const artifact: Artifact = {
  id: 'a1', title: 'Report', directory: '/repo', remoteId: 'local', createdAt: '2026-09-01T10:00:00Z',
  items: [
    { kind: 'file', name: 'big.bin', mime: 'application/octet-stream', size: 40 << 20 },
    { kind: 'link', url: 'https://ci.test/run/1', label: 'CI' },
  ],
};

describe('ArtifactShareModal', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    vi.mocked(artifactsApi.shares).mockResolvedValue({
      relayConfigured: true, maxShareBytes: 32 << 20,
      shares: [{ id: 's1', url: 'https://relay.test/v/r1#k=old', createdAt: 1 }, { id: 's0', url: 'https://relay.test/v/r0#k=gone', createdAt: 0, revokedAt: 2 }],
    });
  });

  it('lists exposed links and sizes against the limit, and creates, copies and revokes shares', async () => {
    const user = userEvent.setup();
    vi.mocked(artifactsApi.share).mockResolvedValue({ id: 's2', url: 'https://relay.test/v/r2#k=new', createdAt: 3 });
    vi.mocked(artifactsApi.revokeShare).mockResolvedValue(undefined);
    render(<ArtifactShareModal artifact={artifact} onClose={() => {}} />);

    expect(screen.getByText('https://ci.test/run/1')).toBeInTheDocument();
    expect(screen.getByText(/big\.bin · 40 MiB/)).toBeInTheDocument();
    await waitFor(() => expect(screen.getAllByLabelText('Share link')).toHaveLength(1));
    expect(screen.getByTestId('artifact-share-size')).toHaveClass('artifact-error');
    expect(screen.getByTestId('artifact-share-size')).toHaveTextContent('of the 32 MiB default relay limit');

    await user.click(screen.getByRole('button', { name: 'Create share link' }));
    await waitFor(() => expect(screen.getAllByLabelText('Share link')[0]).toHaveValue('https://relay.test/v/r2#k=new'));

    await user.click(screen.getAllByRole('button', { name: 'Copy' })[0]);
    expect(copyTextToClipboard).toHaveBeenCalledWith('https://relay.test/v/r2#k=new');

    await user.click(screen.getAllByRole('button', { name: 'Revoke' })[1]);
    expect(artifactsApi.revokeShare).toHaveBeenCalledWith('a1', 's1');
    await waitFor(() => expect(screen.getAllByLabelText('Share link')).toHaveLength(1));
  });

  it('shows the relay refusal and disables sharing without a relay', async () => {
    const user = userEvent.setup();
    vi.mocked(artifactsApi.share).mockRejectedValue(new Error('this artifact is too large for the share relay'));
    render(<ArtifactShareModal artifact={artifact} onClose={() => {}} />);
    const create = await screen.findByRole('button', { name: 'Create share link' });
    await waitFor(() => expect(create).toBeEnabled());
    await user.click(create);
    expect(await screen.findByRole('alert')).toHaveTextContent('too large');

    vi.mocked(artifactsApi.shares).mockResolvedValue({ relayConfigured: false, maxShareBytes: 1, shares: [] });
    render(<ArtifactShareModal artifact={{ ...artifact, id: 'a2' }} onClose={() => {}} />);
    expect(await screen.findByText('No share relay is configured.')).toBeInTheDocument();
  });
});
