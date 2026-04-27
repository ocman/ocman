import { Component } from 'react';
import type { ErrorInfo, ReactNode } from 'react';
import { remoteLog } from '../lib/remoteLog';

// FallbackRender lets callers render a custom fallback while still getting
// access to the captured error and a way to clear it. Returning a node from
// here is treated identically to passing `fallback` directly.
export type FallbackRender = (args: {
  error: Error;
  reset: () => void;
}) => ReactNode;

export interface ErrorBoundaryProps {
  children: ReactNode;
  /**
   * Identifier used in remote logs to distinguish boundaries (e.g. "app",
   * "right-panel:session", "thread"). Defaults to "anonymous".
   */
  name?: string;
  /**
   * Static fallback element. When provided it takes precedence over the
   * default fallback UI. Use `fallbackRender` instead if you need access
   * to the error or a reset handle.
   */
  fallback?: ReactNode;
  /**
   * Render-prop fallback. Receives the captured error and a reset
   * callback that clears the boundary so children re-mount.
   */
  fallbackRender?: FallbackRender;
  /**
   * When `inline` is true the default fallback uses a compact variant
   * suited to embedding inside a panel rather than filling the viewport.
   */
  inline?: boolean;
  /**
   * Auto-reset trigger. Whenever the resetKey value changes between
   * renders the boundary clears its captured error. Useful for keying on
   * `location.pathname` or a session id so navigating away from a crashed
   * region recovers without a manual reload.
   */
  resetKey?: unknown;
  /**
   * Optional callback fired after the boundary catches an error. Runs in
   * addition to the built-in `remoteLog.error` reporting, so callers can
   * hook in extra telemetry without losing the default behaviour.
   */
  onError?: (error: Error, info: ErrorInfo) => void;
  /**
   * Optional callback fired when the boundary's error is cleared (either
   * via the user clicking Try again, or because resetKey changed).
   */
  onReset?: () => void;
}

interface ErrorBoundaryState {
  error: Error | null;
}

/**
 * ErrorBoundary catches render-time exceptions in its subtree, reports them
 * to the server log via remoteLog, and shows a fallback UI in place of the
 * crashed children. A boundary recovers when:
 *   - the user clicks the Try again button (default fallback), or
 *   - the parent passes a new `resetKey` value (typically the route path or
 *     session id), or
 *   - the caller invokes `reset()` from a `fallbackRender` prop.
 *
 * Multiple, narrowly-scoped boundaries are preferred over a single
 * top-level catch-all: a crash in one panel should not blank the rest of
 * the app.
 */
export class ErrorBoundary extends Component<ErrorBoundaryProps, ErrorBoundaryState> {
  state: ErrorBoundaryState = { error: null };

  static getDerivedStateFromError(error: Error): ErrorBoundaryState {
    return { error };
  }

  componentDidUpdate(prevProps: ErrorBoundaryProps) {
    // Auto-reset on resetKey change so route navigation clears stale crashes.
    if (this.state.error && prevProps.resetKey !== this.props.resetKey) {
      this.reset();
    }
  }

  componentDidCatch(error: Error, info: ErrorInfo) {
    const name = this.props.name ?? 'anonymous';
    // Forward to the remote log so render-time crashes show up server-side
    // even when the user can't reach devtools (iPad / installed PWA).
    remoteLog.error(`ErrorBoundary[${name}] caught error`, {
      message: error.message,
      stack: error.stack,
      componentStack: info.componentStack,
    });
    this.props.onError?.(error, info);
  }

  reset = () => {
    if (this.state.error) {
      this.setState({ error: null });
      this.props.onReset?.();
    }
  };

  render() {
    const { error } = this.state;
    if (!error) return this.props.children;

    if (this.props.fallbackRender) {
      return this.props.fallbackRender({ error, reset: this.reset });
    }
    if (this.props.fallback !== undefined) {
      return this.props.fallback;
    }

    const className = this.props.inline
      ? 'oc-error-boundary inline'
      : 'oc-error-boundary';
    return (
      <div className={className} role="alert">
        <h2>Something went wrong</h2>
        <p>{error.message || 'An unexpected error occurred while rendering this view.'}</p>
        <button type="button" onClick={this.reset}>Try again</button>
      </div>
    );
  }
}
