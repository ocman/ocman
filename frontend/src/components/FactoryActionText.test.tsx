// @vitest-environment jsdom
import { fireEvent, render, screen } from '@testing-library/react';
import { describe, expect, it } from 'vitest';
import { FactoryActionText } from './FactoryActionText';

describe('FactoryActionText', () => {
  it('renders Markdown, clamps to seven lines, and expands by click or keyboard', () => {
    render(<FactoryActionText text={'**Recovery**\n\n' + Array.from({ length: 10 }, (_, i) => `- Line ${i + 1}`).join('\n')} />);
    const preview = screen.getByRole('button', { name: 'Expand action text' });
    expect(screen.getByText('Recovery').tagName).toBe('STRONG');
    expect(screen.getAllByRole('listitem')).toHaveLength(10);
    expect(preview.style.webkitLineClamp).toBe('7');
    fireEvent.click(screen.getByText('Recovery'));
    expect(preview).toHaveAttribute('aria-expanded', 'true');
    expect(preview.style.webkitLineClamp).toBe('');
    fireEvent.keyDown(preview, { key: 'Enter' });
    expect(preview).toHaveAttribute('aria-expanded', 'false');
    fireEvent.keyDown(preview, { key: ' ' });
    expect(preview).toHaveAttribute('aria-expanded', 'true');
    fireEvent.keyDown(preview, { key: 'Escape' });
    expect(preview).toHaveAttribute('aria-expanded', 'true');
  });

  it('keeps Markdown links clickable without toggling the preview', () => {
    render(<FactoryActionText text="Read [the evidence](https://example.com)" />);
    fireEvent.click(screen.getByRole('link', { name: 'the evidence' }));
    expect(screen.getByRole('button', { name: 'Expand action text' })).toHaveAttribute('aria-expanded', 'false');
  });
});
