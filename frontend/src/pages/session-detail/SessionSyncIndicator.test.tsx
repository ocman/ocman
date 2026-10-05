// @vitest-environment jsdom
import { describe, expect, it, vi } from 'vitest';
import { fireEvent, render, screen } from '@testing-library/react';
import { SessionSyncIndicator } from './SessionSyncIndicator';

describe('SessionSyncIndicator', () => {
  it('renders nothing when the view is current', () => {
    const { container } = render(<SessionSyncIndicator refreshing={false} refreshError={null} onRetry={() => {}} />);
    expect(container).toBeEmptyDOMElement();
  });

  it('shows a checking note while refreshing', () => {
    render(<SessionSyncIndicator refreshing refreshError={null} onRetry={() => {}} />);
    expect(screen.getByRole('status')).toHaveTextContent('Checking for updates');
  });

  it('reports a failed refresh with a retry', () => {
    const onRetry = vi.fn();
    render(<SessionSyncIndicator refreshing={false} refreshError="offline" onRetry={onRetry} />);
    expect(screen.getByRole('alert')).toHaveTextContent('offline');
    fireEvent.click(screen.getByRole('button', { name: 'Retry' }));
    expect(onRetry).toHaveBeenCalledOnce();
  });
});
