// @vitest-environment jsdom

import { render, screen } from '@testing-library/react';
import { expect, it, vi } from 'vitest';
import { ChangesListPane } from './ChangesListPane';

const baseProps = {
  enabled: true,
  hasData: true,
  loading: false,
  error: null,
  refresh: vi.fn(),
  summary: { files: 0, additions: 0, deletions: 0 },
  fullscreenTitle: 'Changes',
  fullscreenFiles: [],
  loadErrorLabel: 'Failed to load changes',
  emptyMessage: 'Nothing here.',
  renderRows: (visible: string[]) => <ul>{visible.map((f) => <li key={f}>{f}</li>)}</ul>,
};

it('shows a skeleton while the first load is in flight', () => {
  render(<ChangesListPane {...baseProps} files={[]} hasData={false} loading />);
  expect(screen.getByLabelText('Loading file list')).toBeInTheDocument();
  expect(screen.queryByText('Nothing here.')).not.toBeInTheDocument();
});

it('shows the empty message and initial load errors', () => {
  const { rerender } = render(<ChangesListPane {...baseProps} files={[]} />);
  expect(screen.getByText('Nothing here.')).toHaveClass('oc-changes-sidebar-empty');

  rerender(<ChangesListPane {...baseProps} files={[]} hasData={false} error="boom" />);
  expect(screen.getByRole('alert')).toHaveTextContent('Failed to load changes: boom');
});

it('suppresses the empty state and list when disabled, showing the notice', () => {
  render(<ChangesListPane {...baseProps} files={['a.ts']} enabled={false} notice={<div>Unsupported</div>} />);
  expect(screen.getByText('Unsupported')).toBeInTheDocument();
  expect(screen.queryByText('a.ts')).not.toBeInTheDocument();
});

it('mounts the first chunk of rows and a sentinel for the rest', () => {
  const files = Array.from({ length: 25 }, (_, i) => `f${i}.ts`);
  const onSummaryChange = vi.fn();
  const { container } = render(
    <ChangesListPane {...baseProps} files={files} summary={{ files: 25, additions: 3, deletions: 1 }} onSummaryChange={onSummaryChange} />,
  );
  expect(screen.getAllByRole('listitem')).toHaveLength(20);
  expect(container.querySelector('.oc-diff-sentinel')).not.toBeNull();
  expect(onSummaryChange).toHaveBeenCalledWith({ files: 25, additions: 3, deletions: 1 });
});
