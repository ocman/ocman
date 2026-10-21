import { useEffect, type ReactNode } from 'react';
import { useStartupStatus, type StartupStatus } from '../lib/startupStatus';

// This endpoint is served before databases, auth and application routes exist.
// Keep it outside the authenticated API client: startup never implies login.
export function StartupMonitor({ children }: { children: ReactNode }) {
  useEffect(() => {
    let stopped = false;
    let timer: ReturnType<typeof setTimeout>;
    let controller: AbortController;
    const poll = async () => {
      controller = new AbortController();
      const timeout = setTimeout(() => controller.abort(), 3000);
      try {
        const response = await fetch('/api/startup', { cache: 'no-store', signal: controller.signal });
        // Standalone/test servers without a startup gateway are already ready.
        const next: StartupStatus = response.status === 404
          ? { state: 'ready', phase: 'Ready', completed: 0, total: 0, elapsedSeconds: 0 }
          : await response.json();
        if (response.status !== 404 && (!response.ok || !['starting', 'ready', 'failed'].includes(next.state))) {
          throw new Error('Startup status unavailable');
        }
        if (!stopped) {
          useStartupStatus.setState({ status: next, unreachable: false });
        }
      } catch {
        if (!stopped) useStartupStatus.setState({ unreachable: true });
      } finally {
        clearTimeout(timeout);
        if (!stopped) timer = setTimeout(() => void poll(), 2000);
      }
    };
    void poll();
    return () => { stopped = true; clearTimeout(timer); controller?.abort(); };
  }, []);

  // AuthGate still enforces login. Startup and reconnects must never unmount
  // the shell, discard composer state or steal focus from an active input.
  return children;
}
