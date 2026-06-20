// @vitest-environment jsdom
import { describe, it, expect, afterEach } from 'vitest';
import { render, screen, fireEvent, cleanup } from '@testing-library/react';
import { IssueRow } from './IssueRow';
import type { Issue } from '../../lib/upstreamApi';

function makeIssue(overrides: Partial<Issue> = {}): Issue {
  return {
    number: 7,
    title: 'Something is broken',
    body: '',
    author: 'carol',
    status: 'open',
    updatedAt: '2025-01-01T00:00:00Z',
    labels: null,
    assignees: null,
    url: 'https://example.com/issues/7',
    host: 'example.com',
    repo: 'dries/ocman',
    ...overrides,
  };
}

afterEach(() => cleanup());

describe('IssueRow', () => {
  it('renders the number, title, and status', () => {
    render(<IssueRow issue={makeIssue()} directory="/repo" remote="origin" />);
    expect(screen.getByTestId('issue-row-7')).toBeInTheDocument();
    expect(screen.getByText('#7')).toBeInTheDocument();
    expect(screen.getByText('Something is broken')).toBeInTheDocument();
    expect(screen.getByText('open')).toBeInTheDocument();
  });

  it('is collapsed by default and expands on summary click', () => {
    render(<IssueRow issue={makeIssue()} directory="/repo" remote="origin" />);
    expect(screen.queryByTestId('issue-detail-7')).not.toBeInTheDocument();

    fireEvent.click(screen.getByRole('button', { expanded: false }));
    expect(screen.getByTestId('issue-detail-7')).toBeInTheDocument();
  });

  it('renders the body as markdown when present', () => {
    render(
      <IssueRow issue={makeIssue({ body: 'Steps to **reproduce**' })} directory="/repo" remote="origin" />,
    );
    fireEvent.click(screen.getByRole('button', { expanded: false }));
    expect(screen.getByText('reproduce')).toBeInTheDocument();
  });

  it('shows "No description." when the body is empty', () => {
    render(<IssueRow issue={makeIssue({ body: '' })} directory="/repo" remote="origin" />);
    fireEvent.click(screen.getByRole('button', { expanded: false }));
    expect(screen.getByText('No description.')).toBeInTheDocument();
  });

  it('exposes an open-in-browser link that targets the issue URL on the forge host', () => {
    render(<IssueRow issue={makeIssue()} directory="/repo" remote="origin" />);
    const open = screen.getByTestId('issue-row-7-open');
    expect(open).toHaveAttribute('href', 'https://example.com/issues/7');
    expect(open).toHaveAttribute('target', '_blank');
    expect(open.getAttribute('aria-label')).toContain('example.com');
  });
});
