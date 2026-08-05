// @vitest-environment jsdom
import { describe, expect, it } from 'vitest';
import { render } from '@testing-library/react';
import { StatusBadge } from './StatusBadge';

describe('StatusBadge', () => {
  it('labels every status, including interrupted', () => {
    for (const [status, label] of [
      ['waiting', 'Waiting'],
      ['busy', 'Busy'],
      ['done', 'Done'],
      ['error', 'Error'],
      ['interrupted', 'Interrupted'],
    ] as const) {
      const { container, unmount } = render(<StatusBadge status={status} />);
      const badge = container.querySelector(`.status-indicator.status-${status}`);
      expect(badge, status).not.toBeNull();
      expect(badge?.textContent).toBe(label);
      unmount();
    }
  });

  // "Interrupted" on its own doesn't say why, so the badge explains it.
  it('explains interrupted in the tooltip', () => {
    const { container } = render(<StatusBadge status="interrupted" />);
    expect(container.querySelector('.status-indicator')?.getAttribute('title')).toContain(
      'stopped before the turn finished',
    );
  });

  it('leaves the tooltip off for self-explanatory statuses', () => {
    const { container } = render(<StatusBadge status="busy" />);
    expect(container.querySelector('.status-indicator')?.getAttribute('title')).toBeNull();
  });

  it('renders an unknown status verbatim rather than blank', () => {
    const { container } = render(<StatusBadge status="something-new" />);
    expect(container.querySelector('.status-indicator')?.textContent).toBe('something-new');
  });

  it('renders a compact dot carrying the status and seen classes', () => {
    const { container } = render(<StatusBadge status="interrupted" compact seen />);
    const dot = container.querySelector('.status-dot-compact');
    expect(dot?.className).toContain('status-interrupted');
    expect(dot?.className).toContain('status-seen');
    expect(dot?.getAttribute('title')).toContain('stopped before the turn finished');
  });

  it('shows the attention icon for a pending prompt instead of the status', () => {
    const { container } = render(<StatusBadge status="busy" compact pending />);
    expect(container.querySelector('.status-icon-compact.status-pending')).not.toBeNull();
    expect(container.querySelector('.status-dot-compact')).toBeNull();
  });

  it('shows an error icon in compact mode', () => {
    const { container } = render(<StatusBadge status="error" compact />);
    expect(container.querySelector('.status-icon-compact.status-error')).not.toBeNull();
  });

  it('prefers a draft ring and its own tooltip over the status', () => {
    const { container } = render(<StatusBadge status="waiting" compact draft />);
    const dot = container.querySelector('.status-dot-compact');
    expect(dot?.className).toContain('has-draft');
    expect(dot?.getAttribute('title')).toBe('Unsent draft');
  });

  it('lets titleOverride win over the derived tooltip', () => {
    const { container } = render(
      <StatusBadge status="interrupted" compact titleOverride="Rate limited" />,
    );
    expect(container.querySelector('.status-dot-compact')?.getAttribute('title')).toBe(
      'Rate limited',
    );
  });

  it('renders the non-compact pending badge', () => {
    const { container } = render(<StatusBadge status="busy" pending />);
    expect(container.querySelector('.status-indicator.status-pending')?.textContent).toBe('Prompt');
  });
});
