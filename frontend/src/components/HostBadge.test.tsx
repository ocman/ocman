// @vitest-environment jsdom
import { describe, expect, it, vi } from 'vitest';
import { render, screen } from '@testing-library/react';
import { HostBadge } from './HostBadge';
// @ts-expect-error Vitest runs in Node; application types intentionally exclude Node globals.
import { readFileSync } from 'node:fs';

// useMultiHost gates the badge; control it per test.
const multiHost = vi.fn();
vi.mock('../lib/useCapabilities', () => ({
  useMultiHost: () => multiHost(),
}));

describe('HostBadge', () => {
  it('scales the icon to a square within the badge without flex compression', () => {
    multiHost.mockReturnValue(false);
    const style = document.createElement('style');
    style.textContent = readFileSync('src/components/HostBadge.css', 'utf8');
    document.head.appendChild(style);
    try {
      render(<HostBadge remoteId="r1" remoteName="Build host" alwaysShow showIcon />);
      const icon = screen.getByLabelText('Build host').querySelector('.bi-hdd-network')!;
      const computed = getComputedStyle(icon);
      expect(computed.width).toBe('1em');
      expect(computed.height).toBe('1em');
      expect(computed.fontSize).toBe('1.2em');
      expect(computed.flexShrink).toBe('0');
    } finally {
      style.remove();
    }
  });
  it('can replace the status dot with a machine icon while retaining offline information', () => {
    multiHost.mockReturnValue(false);
    render(<HostBadge remoteId="r1" remoteName="Build host" alwaysShow showIcon stale />);
    const badge = screen.getByLabelText('Build host (offline — last known)');
    expect(badge).toHaveClass('host-badge--icon', 'stale');
    expect(badge).toHaveTextContent('Build host (offline)');
    expect(badge.querySelector('.bi-hdd-network')).toHaveAttribute('aria-hidden', 'true');
  });
  it('can show the local owner in a styled project header on a single-host install', () => {
    multiHost.mockReturnValue(false);
    render(<HostBadge remoteId="local" remoteName="This machine" alwaysShow className="header-machine-pill" />);
    expect(screen.getByLabelText('This machine')).toHaveClass('host-badge', 'header-machine-pill');
  });
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
