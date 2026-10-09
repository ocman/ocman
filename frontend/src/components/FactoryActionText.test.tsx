// @vitest-environment jsdom
import { fireEvent, render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { describe, expect, it } from 'vitest';
import { FactoryActionText } from './FactoryActionText';

describe('FactoryActionText', () => {
  it('renders Markdown, clamps to seven lines, and expands by click or keyboard', async () => {
    const user = userEvent.setup();
    render(<FactoryActionText text={'**Recovery**\n\n' + Array.from({ length: 10 }, (_, i) => `- Line ${i + 1}`).join('\n')} />);
    const toggle = screen.getByRole('button', { name: 'Expand action text' });
    const preview = screen.getByTestId('factory-action-text');
    expect(screen.getByText('Recovery').tagName).toBe('STRONG');
    expect(screen.getByText('Recovery').closest('[role="button"], button')).toBeNull();
    expect(screen.getAllByRole('listitem')).toHaveLength(10);
    expect(preview.style.webkitLineClamp).toBe('7');
    fireEvent.click(screen.getByText('Recovery'));
    expect(toggle).toHaveAttribute('aria-expanded', 'true');
    expect(preview.style.webkitLineClamp).toBe('');
    toggle.focus();
    await user.keyboard('{Enter}');
    expect(toggle).toHaveAttribute('aria-expanded', 'false');
    await user.keyboard(' ');
    expect(toggle).toHaveAttribute('aria-expanded', 'true');
    await user.keyboard('{Escape}');
    expect(toggle).toHaveAttribute('aria-expanded', 'true');
  });

  it('keeps Markdown links clickable without toggling the preview', () => {
    render(<FactoryActionText text="Read [the evidence](https://example.com)" />);
    fireEvent.click(screen.getByRole('link', { name: 'the evidence' }));
    expect(screen.getByRole('button', { name: 'Expand action text' })).toHaveAttribute('aria-expanded', 'false');
  });
});
