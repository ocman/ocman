// @vitest-environment jsdom
import { render, screen } from '@testing-library/react';
import { describe, expect, it } from 'vitest';
import { LoadingState } from './LoadingState';
import { Spinner } from './Spinner';

describe('loading indicators', () => {
  it('announces the visible loading text once and keeps the spinner decorative', () => {
    render(<LoadingState className="custom-loading">Loading projects...</LoadingState>);
    const status = screen.getByRole('status');
    expect(status).toHaveTextContent('Loading projects...');
    expect(status).toHaveClass('custom-loading');
    expect(status).not.toHaveAttribute('aria-label');
    expect(status).not.toHaveAttribute('aria-live');
    expect(status.firstElementChild).toHaveAttribute('aria-hidden', 'true');
    expect(screen.getAllByRole('status')).toHaveLength(1);
    expect(screen.queryByRole('progressbar')).not.toBeInTheDocument();
  });

  it('does not add an announcement or focus target next to a labelled control', () => {
    render(<button><Spinner className="custom-spinner" />Saving</button>);
    const button = screen.getByRole('button', { name: 'Saving' });
    expect(button.firstElementChild).toHaveClass('custom-spinner');
    expect(button.firstElementChild).toHaveAttribute('aria-hidden', 'true');
    expect(button.firstElementChild).not.toHaveAttribute('tabindex');
    expect(screen.queryByRole('status')).not.toBeInTheDocument();
  });
});
