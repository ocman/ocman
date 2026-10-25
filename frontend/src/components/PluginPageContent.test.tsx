// @vitest-environment jsdom
import { fireEvent, render, screen } from '@testing-library/react';
import { MemoryRouter } from 'react-router-dom';
import { expect, it, vi } from 'vitest';
import { PluginPageContent } from './PluginPageContent';
import type { PageDocument } from '../lib/pluginPages';

it('renders host-owned detail values and empty lists', () => {
  const doc: PageDocument = { revision: 'r', fields: [], values: {}, tree: { available: false },
    detail: { title: 'Details', properties: [
      { label: 'Enabled', display: 'boolean', value: true }, { label: 'Disabled', display: 'boolean', value: false },
      { label: 'Count', display: 'number', value: 3 }, { label: 'State', display: 'status', value: 'in_progress' },
    ] }, collection: { title: 'Jobs', columns: [{ key: 'name', label: 'Name', display: 'text' }], rows: [], nextCursor: 'next' } };
  const onCursor = vi.fn();
  const { rerender } = render(<MemoryRouter><PluginPageContent page={{ ownerId: 'local', pluginId: 'example', page: { id: 'jobs', label: 'Jobs' } }} projectId="p" document={doc} disabled={false} cursor="previous" onCursor={onCursor} /></MemoryRouter>);
  expect(screen.getByText('Yes')).toBeInTheDocument();
  expect(screen.getByText('No')).toBeInTheDocument();
  expect(screen.getByText('in progress')).toBeInTheDocument();
  expect(screen.getByText('No items.')).toBeInTheDocument();
  fireEvent.click(screen.getByRole('button', { name: 'Next page' }));
  expect(onCursor).toHaveBeenCalledWith('next');
  doc.collection!.rows = [{ id: 'one', values: { name: 'Job one' } }];
  rerender(<MemoryRouter><PluginPageContent page={{ ownerId: 'local', pluginId: 'example', page: { id: 'jobs', label: 'Jobs' } }} projectId="p" document={doc} disabled cursor="" onCursor={onCursor} /></MemoryRouter>);
  expect(screen.queryByRole('link')).not.toBeInTheDocument();
  expect(screen.getByText(/Save or discard edits/)).toBeInTheDocument();
});
