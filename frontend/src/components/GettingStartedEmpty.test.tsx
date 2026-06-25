// @vitest-environment jsdom
import { describe, it, expect, vi } from 'vitest';
import { render, screen, fireEvent } from '@testing-library/react';

// The component selects openProjectPalette from the persisted uiStore.
// Mock the hook so the click path doesn't hit the persist middleware
// (jsdom's localStorage is read-only under vitest).
const openProjectPalette = vi.fn();
vi.mock('../lib/uiStore', () => ({
  useUiStore: (selector: (s: { openProjectPalette: () => void }) => unknown) =>
    selector({ openProjectPalette }),
}));

import { GettingStartedEmpty } from './GettingStartedEmpty';

describe('GettingStartedEmpty', () => {
  it('renders the empty-state guidance and new-session button', () => {
    render(<GettingStartedEmpty />);
    expect(screen.getByTestId('getting-started-empty')).toBeInTheDocument();
    expect(screen.getByText('No sessions yet')).toBeInTheDocument();
    expect(screen.getByRole('button', { name: '+ New project' })).toBeInTheDocument();
  });

  it('opens the project palette when the button is clicked', () => {
    render(<GettingStartedEmpty />);
    fireEvent.click(screen.getByRole('button', { name: '+ New project' }));
    expect(openProjectPalette).toHaveBeenCalledOnce();
  });

  it('left-aligns content in the compact variant', () => {
    render(<GettingStartedEmpty compact />);
    expect(screen.getByTestId('getting-started-empty')).toHaveStyle({ textAlign: 'left' });
  });

  it('centers content in the full variant', () => {
    render(<GettingStartedEmpty />);
    expect(screen.getByTestId('getting-started-empty')).toHaveStyle({ textAlign: 'center' });
  });
});
