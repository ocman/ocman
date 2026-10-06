// @vitest-environment jsdom
import { describe, expect, it, vi } from 'vitest';
import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { SecretField } from './SecretField';

describe('SecretField', () => {
  it('masks the value until the eye is toggled', async () => {
    const user = userEvent.setup();
    render(<label>Secret<SecretField defaultValue="Bearer s" /></label>);
    const input = screen.getByLabelText('Secret');
    expect(input).toHaveAttribute('type', 'password');
    await user.click(screen.getByRole('button', { name: 'Show secret' }));
    expect(input).toHaveAttribute('type', 'text');
    expect(screen.getByRole('button', { name: 'Hide secret' })).toHaveAttribute('aria-pressed', 'true');
    await user.click(screen.getByRole('button', { name: 'Hide secret' }));
    expect(input).toHaveAttribute('type', 'password');
  });

  it('can disable reveal without leaving an already revealed value visible', async () => {
    const user = userEvent.setup();
    const { rerender } = render(<SecretField aria-label="Token" value="new-token" onChange={() => {}} />);
    await user.click(screen.getByRole('button', { name: 'Show Token' }));
    rerender(<SecretField aria-label="Token" value="new-token" onChange={() => {}} allowReveal={false} />);
    expect(screen.getByLabelText('Token')).toHaveAttribute('type', 'password');
    expect(screen.queryByRole('button')).not.toBeInTheDocument();
  });

  it('distinguishes a protected blank from an explicit reset and does not submit the form', async () => {
    const user = userEvent.setup();
    const onReset = vi.fn();
    const onSubmit = vi.fn();
    const { rerender } = render(<form onSubmit={onSubmit}><SecretField aria-label="Token" protect value="" onChange={() => {}} onReset={onReset} /></form>);
    expect(screen.getByLabelText('Token')).toHaveAttribute('placeholder', 'Leave blank to keep');
    expect(screen.getByRole('button', { name: 'Reset Token' })).toHaveAttribute('aria-pressed', 'false');
    await user.click(screen.getByRole('button', { name: 'Reset Token' }));
    expect(onReset).toHaveBeenCalledOnce();
    expect(onSubmit).not.toHaveBeenCalled();
    rerender(<form onSubmit={onSubmit}><SecretField aria-label="Token" protect value="" onChange={() => {}} onReset={onReset} resetPending /></form>);
    expect(screen.getByRole('status')).toHaveTextContent('Will be cleared when saved.');
    expect(screen.getByRole('button', { name: 'Undo reset Token' })).toHaveAttribute('aria-pressed', 'true');
    expect(screen.getByLabelText('Token')).toHaveAccessibleDescription('Will be cleared when saved.');
  });

  it('disables both actions with the field and hides reset when no reset handler exists', async () => {
    const user = userEvent.setup();
    const onReset = vi.fn();
    const { rerender } = render(<SecretField aria-label="Token" disabled onReset={onReset} />);
    for (const button of screen.getAllByRole('button')) expect(button).toBeDisabled();
    await user.click(screen.getByRole('button', { name: 'Reset Token' }));
    expect(onReset).not.toHaveBeenCalled();
    rerender(<SecretField aria-label="Token" />);
    expect(screen.queryByRole('button', { name: 'Reset Token' })).not.toBeInTheDocument();
  });
});
