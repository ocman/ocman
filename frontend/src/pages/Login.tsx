import { useState } from 'react';
import type { FormEvent } from 'react';
import { useAuthStore } from '../lib/authStore';
import './Login.css';

/**
 * Login renders the lockscreen that gates the whole app when the
 * backend has auth configured and this client lacks a valid cookie.
 *
 * It's a plain HTML form — the codebase has no form library or
 * reusable input component, so we stay local and match the house
 * style (squared corners, CSS classes, no inline styles beyond what
 * already exists elsewhere).
 */
export function Login() {
  const submitting = useAuthStore((s) => s.submitting);
  const error = useAuthStore((s) => s.error);
  const login = useAuthStore((s) => s.login);
  const [password, setPassword] = useState('');

  async function onSubmit(e: FormEvent) {
    e.preventDefault();
    if (!password) return;
    const ok = await login(password);
    if (ok) {
      setPassword('');
    }
  }

  return (
    <div className="oc-login">
      <div className="oc-login-card">
        <h2 className="oc-login-title">ocman</h2>
        <p className="oc-login-subtitle">Enter password to continue.</p>
        <form className="oc-login-form" onSubmit={onSubmit}>
          <input
            type="password"
            className="oc-login-input"
            value={password}
            onChange={(e) => setPassword(e.target.value)}
            placeholder="Password"
            autoFocus
            autoComplete="current-password"
            disabled={submitting}
          />
          {error && (
            <div className="oc-error-banner" role="alert">
              {error}
            </div>
          )}
          <button
            type="submit"
            className="oc-login-submit"
            disabled={submitting || !password}
          >
            {submitting ? 'Signing in…' : 'Sign in'}
          </button>
        </form>
      </div>
    </div>
  );
}
