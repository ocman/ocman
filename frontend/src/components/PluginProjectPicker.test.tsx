// @vitest-environment jsdom
import { useState } from 'react';
import { fireEvent, render, screen } from '@testing-library/react';
import { expect, it, vi } from 'vitest';
import { PluginProjectPicker } from './PluginProjectPicker';

const mocks = vi.hoisted(() => ({ error: false }));
vi.mock('../lib/queries', () => ({ useProjects: () => ({ isError: mocks.error, data: [
  { directory: '/srv/web', remoteId: 'machine' }, { directory: '/srv/api', remoteId: 'machine' },
  { directory: '/srv/private', remoteId: 'other' }, { directory: '/srv/local' },
] }) }));

function Picker({ initial = '' }: { initial?: string }) {
  const [value, setValue] = useState(initial);
  return <><PluginProjectPicker ownerId="machine" label="Code projects" value={value} onChange={setValue} /><output>{value}</output></>;
}

it('selects multiple owner-local projects with the app picker and prevents duplicates and parent scopes', () => {
  render(<Picker />);
  fireEvent.click(screen.getByRole('combobox', { name: 'Code projects' }));
  expect(screen.queryByRole('option', { name: 'srv/private' })).not.toBeInTheDocument();
  expect(screen.queryByRole('option', { name: 'srv/local' })).not.toBeInTheDocument();
  expect(screen.queryByRole('option', { name: /2 projects/ })).not.toBeInTheDocument();
  fireEvent.click(screen.getByRole('option', { name: 'srv/web' }));
  fireEvent.click(screen.getByRole('combobox', { name: 'Code projects' }));
  expect(screen.queryByRole('option', { name: 'srv/web' })).not.toBeInTheDocument();
  fireEvent.click(screen.getByRole('option', { name: 'srv/api' }));
  expect(screen.getByRole('status')).toHaveTextContent('/srv/web /srv/api');
  fireEvent.click(screen.getByRole('button', { name: 'Remove /srv/web' }));
  expect(screen.getByRole('status')).toHaveTextContent('/srv/api');
  expect(screen.getByRole('status')).not.toHaveTextContent('/srv/web');
});

it('preserves unavailable projects and reports load failures rather than clearing scope', () => {
  mocks.error = true;
  render(<Picker initial="/legacy/code" />);
  expect(screen.getByText('Unavailable on this owner')).toBeInTheDocument();
  expect(screen.getByRole('alert')).toHaveTextContent('Existing selections are preserved');
  expect(screen.getByRole('status')).toHaveTextContent('/legacy/code');
  mocks.error = false;
});
