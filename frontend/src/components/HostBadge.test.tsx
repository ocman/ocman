// @vitest-environment jsdom
import { describe, expect, it, vi } from 'vitest';
import { render, screen } from '@testing-library/react';
import { HostBadge } from './HostBadge';

// useMultiHost gates the badge; control it per test.
const multiHost = vi.fn();
vi.mock('../lib/useCapabilities', () => ({
  useMultiHost: () => multiHost(),
}));

describe('HostBadge', () => {
  it('renders nothing on a single-host install', () => {
    multiHost.mockReturnValue(false);
    const { container } = render(<HostBadge remoteName="Box" remoteId="r1" />);
    expect(container.innerHTML).toBe('');
  });

  it('renders nothing when no remoteId is given', () => {
    multiHost.mockReturnValue(true);
    const { container } = render(<HostBadge remoteId={undefined} />);
    expect(container.innerHTML).toBe('');
  });

  it('renders nothing for the local machine', () => {
    multiHost.mockReturnValue(true);
    const { container } = render(<HostBadge remoteName="This machine" remoteId="local" />);
    expect(container.innerHTML).toBe('');
  });

  it('shows the host name when multiple hosts are present', () => {
    multiHost.mockReturnValue(true);
    render(<HostBadge remoteName="Workstation" remoteId="r1" />);
    expect(screen.getByText('Workstation')).toBeInTheDocument();
  });

  it('flags an offline remote with no name as remote', () => {
    multiHost.mockReturnValue(true);
    render(<HostBadge remoteId="r1" stale />);
    const badge = screen.getByText(/Remote/);
    expect(badge).toHaveClass('stale');
    expect(badge).toHaveTextContent('offline');
  });

  it('marks a stale (offline) host', () => {
    multiHost.mockReturnValue(true);
    render(<HostBadge remoteName="Workstation" remoteId="r1" stale />);
    const badge = screen.getByText(/Workstation/);
    expect(badge).toHaveClass('stale');
    expect(badge).toHaveTextContent('offline');
    expect(badge.getAttribute('title')).toContain('offline');
  });
});
