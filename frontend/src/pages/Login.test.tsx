// @vitest-environment jsdom
import { beforeEach, expect, it, vi } from 'vitest';
import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { Login } from './Login';

const auth = vi.hoisted(() => ({ submitting: false, error: null as string | null, login: vi.fn() }));
vi.mock('../lib/authStore', () => ({ useAuthStore: (select: (state: typeof auth) => unknown) => select(auth) }));
beforeEach(() => {
  auth.submitting = false;
  auth.error = null;
  auth.login.mockReset().mockResolvedValue(true);
});

it('labels the masked password field and prevents empty submission', () => {
  render(<Login />);
  const field = screen.getByLabelText('Password');
  expect(field).toHaveAttribute('type', 'password');
  expect(field).toHaveAttribute('name', 'password');
  expect(field).toHaveAttribute('autocomplete', 'current-password');
  expect(field).toHaveClass('oc-field');
  const button = screen.getByRole('button', { name: 'Sign in' });
  expect(button).toHaveClass('oc-button');
  expect(button).toBeDisabled();
  fireEvent.submit(screen.getByRole('form', { name: 'Sign in' }));
  expect(auth.login).not.toHaveBeenCalled();
});

it('submits the exact password with Enter and clears it on success', async () => {
  const user = userEvent.setup();
  render(<Login />);
  const field = screen.getByLabelText('Password');
  await user.type(field, ' leading and trailing spaces ');
  await user.keyboard('{Enter}');
  expect(auth.login).toHaveBeenCalledOnce();
  expect(auth.login).toHaveBeenCalledWith(' leading and trailing spaces ');
  await waitFor(() => expect(field).toHaveValue(''));
});

it('retains a rejected password and links the field to its error', async () => {
  const user = userEvent.setup();
  auth.login.mockResolvedValue(false);
  const view = render(<Login />);
  const field = screen.getByLabelText('Password');
  await user.type(field, 'incorrect');
  await user.click(screen.getByRole('button', { name: 'Sign in' }));
  auth.error = 'Incorrect password.';
  view.rerender(<Login />);
  expect(field).toHaveValue('incorrect');
  expect(field).toHaveAttribute('aria-invalid', 'true');
  expect(field).toHaveAttribute('aria-describedby', 'login-error');
  expect(screen.getByRole('alert')).toHaveTextContent('Incorrect password.');
});

it('disables the password field and marks the submit action busy during login', () => {
  auth.submitting = true;
  render(<Login />);
  expect(screen.getByLabelText('Password')).toBeDisabled();
  expect(screen.getByRole('button', { name: 'Signing in…' })).toBeDisabled();
  expect(screen.getByRole('button', { name: 'Signing in…' })).toHaveAttribute('aria-busy', 'true');
});
