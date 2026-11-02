// @vitest-environment jsdom
import { describe, expect, it, vi } from 'vitest';
import { fireEvent, render, screen } from '@testing-library/react';
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

  it('offers a pending Retry and a default icon for an unavailable state', () => {
    const onRetry = vi.fn();
    const { container, rerender } = render(<PageState error title="Reports are unavailable" onRetry={onRetry} />);
    fireEvent.click(screen.getByRole('button', { name: 'Retry' }));
    expect(onRetry).toHaveBeenCalledTimes(1);
    expect(container.querySelector('i')).toHaveClass('bi-exclamation-circle');
    expect(container.querySelector('p')).toBeNull();
    rerender(<PageState error title="Reports are unavailable" onRetry={onRetry} retrying />);
    expect(screen.getByRole('button', { name: 'Retry' })).toBeDisabled();
    expect(screen.getByRole('button', { name: 'Retry' })).toHaveAttribute('aria-busy', 'true');
  });

  it('renders a plain empty state without an icon', () => {
    const { container } = render(<PageState title="Nothing here" description="Empty." />);
    expect(container.querySelector('i')).toBeNull();
    expect(container.querySelector('[data-error-state]')).toBeNull();
  });
});
