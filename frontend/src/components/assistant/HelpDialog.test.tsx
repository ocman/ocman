// @vitest-environment jsdom
import { describe, it, expect, vi } from 'vitest';
import { render, screen, fireEvent } from '@testing-library/react';
import { HelpDialog } from './HelpDialog';
import type { SlashCommand } from '../../lib/api';

const commands: SlashCommand[] = [
  { name: 'model', description: 'Change the active model' },
  { name: 'archive', description: 'Archive this session' },
  { name: 'noop' },
];

describe('HelpDialog', () => {
  it('renders nothing when closed', () => {
    const { container } = render(
      <HelpDialog open={false} commands={commands} onClose={() => {}} />,
    );
    expect(container.firstChild).toBeNull();
  });

  it('lists every command with its description, sorted by name', () => {
    render(<HelpDialog open commands={commands} onClose={() => {}} />);
    expect(screen.getByRole('dialog', { name: 'Slash commands' })).toBeInTheDocument();
    expect(screen.getByText('/archive')).toBeTruthy();
    expect(screen.getByText('/model')).toBeTruthy();
    expect(screen.getByText('/noop')).toBeTruthy();
    expect(screen.getByText('Change the active model')).toBeTruthy();

    const titles = screen
      .getAllByText(/^\//)
      .map((el) => el.textContent);
    expect(titles).toEqual(['/archive', '/model', '/noop']);
  });

  it('closes on Escape', () => {
    const onClose = vi.fn();
    render(<HelpDialog open commands={commands} onClose={onClose} />);
    fireEvent.keyDown(window, { key: 'Escape' });
    expect(onClose).toHaveBeenCalledTimes(1);
  });

  it('closes when the backdrop is clicked', () => {
    const onClose = vi.fn();
    render(<HelpDialog open commands={commands} onClose={onClose} />);
    fireEvent.click(document.querySelector('.oc-cmd-backdrop')!);
    expect(onClose).toHaveBeenCalledTimes(1);
  });
});
