// @vitest-environment jsdom
import { render, screen } from '@testing-library/react';
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
