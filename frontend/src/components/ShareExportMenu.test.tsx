// @vitest-environment jsdom
import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { ShareLinkModal } from './ShareExportMenu';

vi.mock('../lib/api', () => ({
  api: {
    listShareLinks: vi.fn(),
    createShareLink: vi.fn(),
    revokeShareLink: vi.fn(),
  },
}));
const copyToClipboard = vi.fn();
vi.mock('../lib/clipboard', () => ({ copyToClipboard: (v: string) => copyToClipboard(v) }));
import { api } from '../lib/api';

const relayLink = { token: 'tok', url: 'https://relay.example.com/v/id#k=key', createdAt: 1 };

describe('ShareLinkModal', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    copyToClipboard.mockResolvedValue(true);
  });

  // A failed publish is nearly always operational (relay down, over the
  // size cap). The modal must show the server's explanation verbatim
  // rather than a generic fallback that hides it.
  it('surfaces the server explanation when creating a link fails', async () => {
    vi.mocked(api.listShareLinks).mockResolvedValue([]);
    vi.mocked(api.createShareLink).mockRejectedValue(
      new Error('share relay http://localhost:8231 is unreachable: connection refused'),
    );
    render(<ShareLinkModal sessionId="s1" onClose={() => {}} />);

    fireEvent.click(await screen.findByTestId('share-create-link'));

    const alert = await screen.findByRole('alert');
    expect(alert).toHaveTextContent('is unreachable');
    expect(alert).toHaveTextContent('http://localhost:8231');
    expect(alert).not.toHaveTextContent('Failed to create share link');
  });

  it('shows only the relay URL', async () => {
    vi.mocked(api.listShareLinks).mockResolvedValue([{
      token: 'tok',
      url: 'https://relay.example.com/v/id#k=key',
      createdAt: 1,
    }]);
    render(<ShareLinkModal sessionId="s1" onClose={() => {}} />);

    expect(await screen.findByLabelText('Relay share URL')).toHaveValue('https://relay.example.com/v/id#k=key');
    expect(screen.queryByLabelText('Local share URL')).not.toBeInTheDocument();
  });

  it('reports a failure to list existing links', async () => {
    vi.mocked(api.listShareLinks).mockRejectedValue(new Error('backend is down'));
    render(<ShareLinkModal sessionId="s1" onClose={() => {}} />);

    expect(await screen.findByRole('alert')).toHaveTextContent('backend is down');
  });

  it('says so when a session has no links yet', async () => {
    vi.mocked(api.listShareLinks).mockResolvedValue([]);
    render(<ShareLinkModal sessionId="s1" onClose={() => {}} />);

    expect(await screen.findByText('No active share links.')).toBeInTheDocument();
  });

  it('lists a newly created link and copies it without a second click', async () => {
    vi.mocked(api.listShareLinks).mockResolvedValue([]);
    vi.mocked(api.createShareLink).mockResolvedValue(relayLink);
    render(<ShareLinkModal sessionId="s1" onClose={() => {}} />);

    fireEvent.click(await screen.findByTestId('share-create-link'));

    expect(await screen.findByLabelText('Relay share URL')).toHaveValue(relayLink.url);
    // The link is useless until it reaches the clipboard, so minting one
    // copies it straight away.
    expect(copyToClipboard).toHaveBeenCalledWith(relayLink.url);
    await waitFor(() => expect(screen.getByTestId('share-copy-link')).toHaveTextContent('Copied!'));
  });

  it('confirms a manual copy', async () => {
    vi.mocked(api.listShareLinks).mockResolvedValue([relayLink]);
    render(<ShareLinkModal sessionId="s1" onClose={() => {}} />);

    fireEvent.click(await screen.findByTestId('share-copy-link'));

    await waitFor(() => expect(screen.getByTestId('share-copy-link')).toHaveTextContent('Copied!'));
  });

  it('warns when the clipboard is unavailable instead of claiming success', async () => {
    vi.mocked(api.listShareLinks).mockResolvedValue([relayLink]);
    copyToClipboard.mockResolvedValue(false);
    render(<ShareLinkModal sessionId="s1" onClose={() => {}} />);

    fireEvent.click(await screen.findByTestId('share-copy-link'));

    expect(await screen.findByRole('alert')).toHaveTextContent('Could not copy to clipboard');
    expect(screen.getByTestId('share-copy-link')).not.toHaveTextContent('Copied!');
  });

  it('drops a revoked link from the list', async () => {
    vi.mocked(api.listShareLinks).mockResolvedValue([relayLink]);
    vi.mocked(api.revokeShareLink).mockResolvedValue(undefined as never);
    render(<ShareLinkModal sessionId="s1" onClose={() => {}} />);

    fireEvent.click(await screen.findByTestId('share-revoke-link'));

    await waitFor(() => expect(screen.queryByLabelText('Relay share URL')).toBeNull());
    expect(api.revokeShareLink).toHaveBeenCalledWith('s1', 'tok');
    expect(await screen.findByText('No active share links.')).toBeInTheDocument();
  });

  it('keeps the link listed when revoking fails', async () => {
    vi.mocked(api.listShareLinks).mockResolvedValue([relayLink]);
    vi.mocked(api.revokeShareLink).mockRejectedValue(new Error('relay refused'));
    render(<ShareLinkModal sessionId="s1" onClose={() => {}} />);

    fireEvent.click(await screen.findByTestId('share-revoke-link'));

    expect(await screen.findByRole('alert')).toHaveTextContent('relay refused');
    // The link is still live on the relay, so it must stay visible.
    expect(screen.getByLabelText('Relay share URL')).toBeInTheDocument();
  });
});
