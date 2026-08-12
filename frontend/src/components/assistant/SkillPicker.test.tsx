// @vitest-environment jsdom
import { render, screen, fireEvent, waitFor } from '@testing-library/react';
import { describe, it, expect, vi, beforeAll } from 'vitest';
import { SkillPicker } from './SkillPicker';
import type { SlashCommand } from '../../lib/api';

const commands: SlashCommand[] = [
  { name: 'init', description: 'setup', source: 'command' },
  { name: 'pr-review', description: 'review a PR', source: 'skill' },
  { name: 'create-commit', description: 'make a commit', source: 'skill' },
  { name: 'codegraph:map', description: 'mcp tool', source: 'mcp' },
  { name: 'legacy', description: 'no source field' },
];

describe('SkillPicker', () => {
  // jsdom lacks scrollIntoView, which CommandListPicker calls on mount.
  beforeAll(() => {
    Element.prototype.scrollIntoView = vi.fn();
  });

  it('lists only source === "skill" commands and skips the rest', () => {
    render(<SkillPicker open commands={commands} onSelect={() => {}} onClose={() => {}} />);

    expect(screen.getByText('pr-review')).toBeInTheDocument();
    expect(screen.getByText('create-commit')).toBeInTheDocument();
    expect(screen.queryByText('init')).not.toBeInTheDocument();
    expect(screen.queryByText('codegraph:map')).not.toBeInTheDocument();
    expect(screen.queryByText('legacy')).not.toBeInTheDocument();
  });

  it('calls onSelect with the bare skill name (composer prefills /<skill>)', () => {
    const onSelect = vi.fn();
    render(<SkillPicker open commands={commands} onSelect={onSelect} onClose={() => {}} />);

    fireEvent.click(screen.getByText('pr-review'));
    expect(onSelect).toHaveBeenCalledWith('pr-review');
  });

  it('exposes the rows as listbox options the search input drives', async () => {
    const onSelect = vi.fn();
    render(<SkillPicker open commands={commands} onSelect={onSelect} onClose={() => {}} />);

    const input = screen.getByRole('combobox');
    await waitFor(() => expect(input).toHaveFocus());
    expect(input).toHaveAttribute('aria-expanded', 'true');

    let options = screen.getAllByRole('option');
    expect(options).toHaveLength(2);
    expect(options[0]).toHaveAttribute('aria-selected', 'true');
    expect(input).toHaveAttribute('aria-activedescendant', options[0].id);

    fireEvent.keyDown(input, { key: 'ArrowDown' });
    options = screen.getAllByRole('option');
    expect(options[1]).toHaveAttribute('aria-selected', 'true');
    expect(input).toHaveAttribute('aria-activedescendant', options[1].id);

    fireEvent.keyDown(input, { key: 'Enter' });
    expect(onSelect).toHaveBeenCalledWith('create-commit');
  });

  it('shows the empty message when no skills are present', () => {
    render(
      <SkillPicker
        open
        commands={[{ name: 'init', source: 'command' }]}
        onSelect={() => {}}
        onClose={() => {}}
      />,
    );
    expect(screen.getByText('No skills found')).toBeInTheDocument();
  });
});
