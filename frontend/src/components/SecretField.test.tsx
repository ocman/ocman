// @vitest-environment jsdom
import { describe, expect, it } from 'vitest';
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
});
