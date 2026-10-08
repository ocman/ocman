// @vitest-environment jsdom
import { render, screen } from '@testing-library/react';
import { MemoryRouter } from 'react-router-dom';
import { describe, expect, it, vi } from 'vitest';
import { MarkdownContent } from './MarkdownText';

// Bound card expansion so the unfixed renderer fails without recursing forever.
vi.mock('../FactoryActionCard', () => ({ FactoryActionCard: () => <span>Expanded approval card</span> }));

describe('prose-only Markdown', () => {
  it.each([
    '[[ocman:card type=factory-epic epic=ship action=approve_plan]]',
    '[Review](/factory/epics/ship?human=1)',
  ])('does not expand a self-referencing rationale: %s', (text) => {
    render(<MemoryRouter><MarkdownContent {...{ text: `## Why\n\n**Missing coverage.**\n\n${text}`, factoryCards: false }} /></MemoryRouter>);
    expect(screen.queryByText('Expanded approval card')).not.toBeInTheDocument();
    expect(screen.getByRole('heading', { name: 'Why' })).toBeVisible();
    expect(screen.getByText('Missing coverage.').tagName).toBe('STRONG');
    if (text.startsWith('[Review]')) expect(screen.getByRole('link', { name: 'Review' })).toHaveAttribute('href', '/factory/epics/ship?human=1');
    else expect(screen.getByText(text)).toBeVisible();
  });

  it('retains requested soft line breaks in prose mode', () => {
    const { container } = render(<MarkdownContent {...{ text: 'First\nSecond', factoryCards: false, preserveLineBreaks: true }} />);
    expect(container.querySelectorAll('br')).toHaveLength(1);
  });
});
