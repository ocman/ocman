// @vitest-environment jsdom
import { describe, it, expect, vi } from 'vitest';
import { render, screen, fireEvent } from '@testing-library/react';
import { QueuedMessages, type QueuedMessageItem } from './QueuedMessages';

const items: QueuedMessageItem[] = [
  { id: 'a', text: 'first', hasImages: false },
  { id: 'b', text: 'second', hasImages: false },
  { id: 'c', text: '', hasImages: true },
];

describe('QueuedMessages', () => {
  it('renders nothing when empty', () => {
    const { container } = render(<QueuedMessages messages={[]} />);
    expect(container.firstChild).toBeNull();
  });

  it('lists each queued message, rendering (image) for image-only entries', () => {
    render(<QueuedMessages messages={items} />);
    expect(screen.getByText('first')).toBeTruthy();
    expect(screen.getByText('second')).toBeTruthy();
    expect(screen.getByText('(image)')).toBeTruthy();
  });

  it('disables Move up on the first and Move down on the last', () => {
    render(<QueuedMessages messages={items} />);
    const ups = screen.getAllByLabelText('Move up');
    const downs = screen.getAllByLabelText('Move down');
    expect((ups[0] as HTMLButtonElement).disabled).toBe(true);
    expect((ups[1] as HTMLButtonElement).disabled).toBe(false);
    expect((downs[items.length - 1] as HTMLButtonElement).disabled).toBe(true);
  });

  it('toggles expanded state when the text is clicked', () => {
    render(<QueuedMessages messages={items} />);
    const textBtn = screen.getByText('first');
    // Collapsed (clamped) by default.
    expect(textBtn.getAttribute('aria-expanded')).toBe('false');
    fireEvent.click(textBtn);
    expect(textBtn.getAttribute('aria-expanded')).toBe('true');
    fireEvent.click(textBtn);
    expect(textBtn.getAttribute('aria-expanded')).toBe('false');
  });

  it('forwards remove and move intents with the right id and direction', () => {
    const onRemove = vi.fn();
    const onMove = vi.fn();
    render(<QueuedMessages messages={items} onRemove={onRemove} onMove={onMove} />);

    fireEvent.click(screen.getAllByLabelText('Remove from queue')[1]);
    expect(onRemove).toHaveBeenCalledWith('b');

    fireEvent.click(screen.getAllByLabelText('Move down')[0]);
    expect(onMove).toHaveBeenCalledWith('a', 1);

    fireEvent.click(screen.getAllByLabelText('Move up')[1]);
    expect(onMove).toHaveBeenCalledWith('b', -1);
  });
});
