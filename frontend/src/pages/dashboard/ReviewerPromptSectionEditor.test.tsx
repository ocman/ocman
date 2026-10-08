// @vitest-environment jsdom
import { fireEvent, render, screen } from '@testing-library/react';
import { expect, it, vi } from 'vitest';
import { ReviewerPromptSectionEditor } from './ReviewerPromptSectionEditor';

it('treats a legacy section as enabled and preserves its other fields when toggled', () => {
  const onChange = vi.fn();
  render(<ReviewerPromptSectionEditor number={1} section={{ title: 'Safety', content: 'Check every command.' }} onChange={onChange} onRemove={vi.fn()} />);
  const toggle = screen.getByRole('checkbox', { name: 'Enable section 1' });
  expect(toggle).toBeChecked();
  fireEvent.click(toggle);
  expect(onChange).toHaveBeenCalledWith({ title: 'Safety', content: 'Check every command.', enabled: false });
});

it('edits the title and instructions using labelled shared fields', () => {
  const onChange = vi.fn();
  render(<ReviewerPromptSectionEditor number={2} section={{ title: 'Safety', content: 'Check commands.', enabled: false }} onChange={onChange} onRemove={vi.fn()} />);
  expect(screen.getByRole('checkbox', { name: 'Enable section 2' })).not.toBeChecked();
  const title = screen.getByRole('textbox', { name: 'Section 2 title' });
  expect(title).toHaveClass('oc-field');
  fireEvent.change(title, { target: { value: 'Updated safety' } });
  expect(onChange).toHaveBeenLastCalledWith({ title: 'Updated safety', content: 'Check commands.', enabled: false });
  const instructions = screen.getByRole('textbox', { name: 'Section 2 instructions' });
  expect(instructions).toHaveClass('oc-field--textarea');
  Object.defineProperties(instructions, { scrollHeight: { value: 120 }, offsetHeight: { value: 80 }, clientHeight: { value: 78 } });
  fireEvent.change(instructions, { target: { value: 'Updated instructions.' } });
  expect(onChange).toHaveBeenLastCalledWith({ title: 'Safety', content: 'Updated instructions.', enabled: false });
  expect(instructions.style.height).toBe('122px');
});

it('uses the shared danger action for explicit removal', () => {
  const remove = vi.fn();
  render(<ReviewerPromptSectionEditor number={1} section={{ title: '', content: '' }} onChange={vi.fn()} onRemove={remove} />);
  const button = screen.getByRole('button', { name: 'Remove section 1' });
  expect(button).toHaveClass('oc-icon-button', 'oc-button--danger');
  fireEvent.click(button);
  expect(remove).toHaveBeenCalledOnce();
});
