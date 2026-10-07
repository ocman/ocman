import { useState } from 'react';
import type { FormEvent } from 'react';
import { useAuthStore } from '../lib/authStore';
import { Button } from '../components/Control';
import { SecretField } from '../components/SecretField';
import { InlineAlert } from '../components/InlineAlert';
import styles from './Login.module.css';

/**
 * Login renders the lockscreen that gates the whole app when the
 * backend has auth configured and this client lacks a valid cookie.
 *
 * The native form sends credentials through the authentication store.
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
    <main className={styles.root}>
      <section className={styles.card} aria-labelledby="login-title">
        <h2 id="login-title" className={styles.title}>ocman</h2>
        <p className={styles.subtitle}>Enter password to continue.</p>
        <form className={styles.form} aria-label="Sign in" onSubmit={onSubmit}>
          <div className={styles.password}>
            <label htmlFor="login-password" className={styles.label}>Password</label>
            <SecretField
              id="login-password"
              name="password"
              allowReveal={false}
              value={password}
              onChange={(e) => setPassword(e.target.value)}
              placeholder="Password"
              autoFocus
              autoComplete="current-password"
              disabled={submitting}
              aria-invalid={Boolean(error)}
              aria-describedby={error ? 'login-error' : undefined}
            />
          </div>
          {error && (
            <InlineAlert><span id="login-error">{error}</span></InlineAlert>
          )}
          <Button
            type="submit"
            variant="accent"
            disabled={submitting || !password}
            aria-busy={submitting}
          >
            {submitting ? 'Signing in…' : 'Sign in'}
          </Button>
        </form>
      </section>
    </main>
  );
}
