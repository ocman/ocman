// @vitest-environment jsdom
import { fireEvent, render, screen } from '@testing-library/react';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { ShareLinkModal } from './ShareExportMenu';

vi.mock('../lib/api', () => ({
  api: {
    listShareLinks: vi.fn(),
    createShareLink: vi.fn(),
    revokeShareLink: vi.fn(),
  },
}));
import { api } from '../lib/api';

describe('ShareLinkModal', () => {
  beforeEach(() => vi.clearAllMocks());

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
});
