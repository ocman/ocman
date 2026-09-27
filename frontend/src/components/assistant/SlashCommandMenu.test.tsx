// @vitest-environment jsdom
import { render, screen } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import { SlashCommandMenu } from './SlashCommandMenu';

describe('SlashCommandMenu', () => {
  it('shows each command type, labelling source-less commands built-in', () => {
    render(
      <SlashCommandMenu
        commands={[
          { name: 'help' },
          { name: 'init', source: 'command' },
          { name: 'pr-review', source: 'skill' },
          { name: 'codegraph:map', source: 'mcp' },
        ]}
        activeIndex={0}
        menuRef={{ current: null }}
        listboxId="menu"
        optionId={(i) => `opt-${i}`}
        onSelect={vi.fn()}
        onHover={vi.fn()}
      />,
    );
    const options = screen.getAllByRole('option');
    expect(options.map((o) => o.querySelector('.oc-slash-kind')?.textContent)).toEqual(['built-in', 'command', 'skill', 'mcp']);
  });
});
