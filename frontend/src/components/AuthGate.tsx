import { useEffect, useState } from 'react';
import type { ReactNode } from 'react';
import { useAuthStore } from '../lib/authStore';
import { Login } from '../pages/Login';
import { BackendStatusBanner } from './BackendStatusBanner';
import { LoadingState } from './LoadingState';
import styles from './AuthGate.module.css';

const AUTH_BOOT_TIMEOUT_MS = 8_000;

/** Keeps the app's initial requests behind the authentication probe. */
export function AuthGate({ children }: { children: ReactNode }) {
  const checking = useAuthStore((s) => s.checking);
  const authRequired = useAuthStore((s) => s.authRequired);
  const authenticated = useAuthStore((s) => s.authenticated);
  const bootstrap = useAuthStore((s) => s.bootstrap);
  const [timedOut, setTimedOut] = useState(false);

  useEffect(() => {
    bootstrap();
  }, [bootstrap]);

  useEffect(() => {
    if (!checking) return;
    const timer = setTimeout(() => setTimedOut(true), AUTH_BOOT_TIMEOUT_MS);
    return () => clearTimeout(timer);
  }, [checking]);

  if (checking) {
    if (timedOut) return <BackendStatusBanner force onRetry={() => void bootstrap()} />;
    return <LoadingState className={styles.checking}>Checking authentication…</LoadingState>;
  }
  if (authRequired && !authenticated) return <Login />;
  return <>{children}</>;
}
