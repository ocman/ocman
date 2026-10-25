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

it('renders summary names, source paths, severity pills and location counts', () => {
  const document: PageDocument = { revision: 'r1', fields: [], values: {}, tree: { available: false }, collection: {
    title: 'Findings', columns: [{ key: 'name', label: 'Name', display: 'summary' }, { key: 'severity', label: 'Severity', display: 'severity' }, { key: 'location', label: 'Location', display: 'summary' }],
    rows: [{ id: 'f1', values: { name: { label: '<script>Race condition</script>', description: 'components/model.go:12' }, severity: 'medium', location: { label: 'platform-service', count: 2 } } },
      { id: 'f2', values: { name: { label: 'Missing authorization' }, severity: 'high', location: { label: 'admin-tools', count: 1 } } }],
  } };
  const { rerender } = render(<MemoryRouter><PluginPageContent page={{ pluginId: 'example.pentest', ownerId: 'local', page: { id: 'runs', label: 'Runs' } }} projectId="p1" document={document} disabled={false} cursor="" onCursor={() => {}} /></MemoryRouter>);
  expect(screen.getAllByRole('columnheader').map((cell) => cell.textContent)).toEqual(['Name', 'Severity', 'Location']);
  expect(screen.getByText('components/model.go:12')).toBeInTheDocument();
  expect(screen.getByText('medium')).toHaveClass('plugin-severity-medium');
  expect(screen.getByText('2 others')).toBeInTheDocument();
  expect(screen.getByText('1 other')).toBeInTheDocument();
  expect(screen.getByRole('link', { name: 'View f1' })).toHaveAttribute('href', '/plugins/example.pentest/runs?ownerId=local&projectId=p1&itemId=f1');
  expect(window.document.querySelector('script')).toBeNull();
  rerender(<MemoryRouter><PluginPageContent page={{ pluginId: 'example.pentest', ownerId: 'local', page: { id: 'runs', label: 'Runs' } }} projectId="p1" document={document} disabled cursor="" onCursor={() => {}} /></MemoryRouter>);
  expect(screen.queryByRole('link')).not.toBeInTheDocument();
  expect(screen.getByText('components/model.go:12')).toBeInTheDocument();
});
