// @vitest-environment jsdom
import { describe, expect, it } from 'vitest';
import { render, screen } from '@testing-library/react';
import { PageState } from './PageState';

describe('PageState', () => {
  it('explains an empty list without announcing an error', () => {
    const { container } = render(<PageState title="No reports yet" description="Saved reports appear here." icon="bi-archive" />);
    expect(screen.getByRole('region', { name: 'No reports yet' })).toBeVisible();
    expect(screen.getByRole('heading', { name: 'No reports yet' })).toBeVisible();
    expect(screen.getByText('Saved reports appear here.')).toBeVisible();
    expect(screen.queryByRole('alert')).not.toBeInTheDocument();
    expect(container.querySelector('i')).toHaveAttribute('aria-hidden', 'true');
  });

  it('announces an unavailable state and accepts recovery actions', () => {
    render(<PageState error title="Reports are unavailable" description="Server offline" icon="bi-exclamation-circle"><button>Retry</button></PageState>);
    expect(screen.getByRole('alert')).toHaveTextContent('Reports are unavailable');
    expect(screen.getByText('Server offline')).toBeVisible();
    expect(screen.getByRole('button', { name: 'Retry' })).toBeVisible();
  });
});
