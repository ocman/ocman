// @vitest-environment jsdom
import { expect, it, vi } from 'vitest';
import { fireEvent, render, screen } from '@testing-library/react';
import { MemoryRouter } from 'react-router-dom';
import type { Session } from '../lib/api';
import { PhoneSessionTools } from './PhoneSessionTools';

vi.mock('./ProjectQuickSettings', () => ({ ProjectQuickSettings: ({ children, directory, remoteId }: { children: React.ReactNode; directory: string; remoteId?: string }) => <div data-testid="project" data-directory={directory} data-owner={remoteId}>{children}</div> }));
vi.mock('./HostBadge', () => ({ HostBadge: () => <span>Machine</span> }));
vi.mock('./ExploreModal', () => ({ ExploreModal: ({ dir, remoteId, onClose }: { dir: string; remoteId: string; onClose: () => void }) => <div role="dialog" aria-label={`${remoteId}:${dir}`}><button onClick={onClose}>Close explorer</button></div> }));

it.each([undefined, 'remote-one'])('opens and closes files on the session owner %s', (remoteId) => {
  render(<MemoryRouter><PhoneSessionTools session={{ directory: '/repos/project', remoteId } as Session} /></MemoryRouter>);
  expect(screen.getByTestId('project')).toHaveAttribute('data-directory', '/repos/project');
  if (remoteId) expect(screen.getByTestId('project')).toHaveAttribute('data-owner', remoteId);
  expect(document.getElementById('phone-session-tools-slot')).toBeInTheDocument();
  fireEvent.click(screen.getByRole('button', { name: 'Explore project files' }));
  expect(screen.getByRole('dialog', { name: `${remoteId || 'local'}:/repos/project` })).toBeInTheDocument();
  fireEvent.click(screen.getByRole('button', { name: 'Close explorer' }));
  expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
});

it('keeps the action slot available while session metadata loads', () => {
  render(<PhoneSessionTools />);
  expect(screen.queryByRole('button')).not.toBeInTheDocument();
  expect(document.getElementById('phone-session-tools-slot')).toBeInTheDocument();
});
