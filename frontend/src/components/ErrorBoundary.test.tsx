import { describe, it, expect, beforeEach, vi } from 'vitest';
import { renderToStaticMarkup } from 'react-dom/server';
import type { ErrorInfo } from 'react';
import { ErrorBoundary } from './ErrorBoundary';

// remoteLog is hit from componentDidCatch. We mock it so the test asserts
// the boundary forwards crashes to the server log without performing a real
// fetch.
vi.mock('../lib/remoteLog', () => ({
  remoteLog: {
    debug: vi.fn(),
    info: vi.fn(),
    warn: vi.fn(),
    error: vi.fn(),
  },
}));

import { remoteLog } from '../lib/remoteLog';

const mockedError = remoteLog.error as ReturnType<typeof vi.fn>;

beforeEach(() => {
  mockedError.mockClear();
});

describe('ErrorBoundary', () => {
  it('renders children when no error occurs', () => {
    const html = renderToStaticMarkup(
      <ErrorBoundary>
        <span>ok</span>
      </ErrorBoundary>,
    );
    expect(html).toContain('ok');
    expect(html).not.toContain('Something went wrong');
  });

  it('captures errors via getDerivedStateFromError', () => {
    const err = new Error('boom');
    const next = ErrorBoundary.getDerivedStateFromError(err);
    expect(next).toEqual({ error: err });
  });

  it('reports caught errors through remoteLog.error and invokes onError', () => {
    const onError = vi.fn();
    const boundary = new ErrorBoundary({
      children: null,
      name: 'unit-test',
      onError,
    });
    const err = new Error('kaboom');
    const info: ErrorInfo = { componentStack: '\n  at Crash' };

    boundary.componentDidCatch(err, info);

    expect(mockedError).toHaveBeenCalledTimes(1);
    expect(mockedError).toHaveBeenCalledWith(
      'ErrorBoundary[unit-test] caught error',
      expect.objectContaining({
        message: 'kaboom',
        componentStack: '\n  at Crash',
      }),
    );
    expect(onError).toHaveBeenCalledWith(err, info);
  });

  it('uses "anonymous" as the default name in remote logs', () => {
    const boundary = new ErrorBoundary({ children: null });
    boundary.componentDidCatch(new Error('x'), { componentStack: '' });
    expect(mockedError).toHaveBeenCalledWith(
      'ErrorBoundary[anonymous] caught error',
      expect.any(Object),
    );
  });

  it('reset() clears the captured error and fires onReset', () => {
    const onReset = vi.fn();
    const boundary = new ErrorBoundary({ children: null, onReset });
    // Simulate React having stored the derived state.
    boundary.state = { error: new Error('crashed') };
    // setState is normally provided by React; stub it for the unit test.
    const setState = vi.fn();
    boundary.setState = setState as unknown as typeof boundary.setState;

    boundary.reset();

    expect(setState).toHaveBeenCalledWith({ error: null });
    expect(onReset).toHaveBeenCalledTimes(1);
  });

  it('reset() is a no-op when there is no captured error', () => {
    const onReset = vi.fn();
    const boundary = new ErrorBoundary({ children: null, onReset });
    boundary.state = { error: null };
    const setState = vi.fn();
    boundary.setState = setState as unknown as typeof boundary.setState;

    boundary.reset();

    expect(setState).not.toHaveBeenCalled();
    expect(onReset).not.toHaveBeenCalled();
  });

  it('auto-resets when resetKey changes after an error', () => {
    const onReset = vi.fn();
    const boundary = new ErrorBoundary({
      children: null,
      resetKey: '/a',
      onReset,
    });
    boundary.state = { error: new Error('stale') };
    const setState = vi.fn();
    boundary.setState = setState as unknown as typeof boundary.setState;

    boundary.componentDidUpdate({
      children: null,
      resetKey: '/b',
    });

    expect(setState).toHaveBeenCalledWith({ error: null });
    expect(onReset).toHaveBeenCalledTimes(1);
  });

  it('does not reset when resetKey is unchanged', () => {
    const onReset = vi.fn();
    const boundary = new ErrorBoundary({
      children: null,
      resetKey: '/same',
      onReset,
    });
    boundary.state = { error: new Error('still crashed') };
    const setState = vi.fn();
    boundary.setState = setState as unknown as typeof boundary.setState;

    boundary.componentDidUpdate({
      children: null,
      resetKey: '/same',
    });

    expect(setState).not.toHaveBeenCalled();
    expect(onReset).not.toHaveBeenCalled();
  });

  it('renders the inline-variant fallback when inline=true and an error is captured', () => {
    // Render a boundary whose initial state already has an error so we don't
    // need a DOM environment to trigger it. React's renderToStaticMarkup
    // calls render() directly, which reads state.error.
    class PreErrored extends ErrorBoundary {
      override state = { error: new Error('display error') };
    }
    const html = renderToStaticMarkup(
      <PreErrored inline>
        <span>hidden</span>
      </PreErrored>,
    );
    expect(html).toContain('oc-error-boundary inline');
    expect(html).toContain('display error');
    expect(html).toContain('Try again');
    expect(html).not.toContain('hidden');
  });

  it('honours the static fallback prop over the default UI', () => {
    class PreErrored extends ErrorBoundary {
      override state = { error: new Error('hidden') };
    }
    const html = renderToStaticMarkup(
      <PreErrored fallback={<div className="custom-fb">custom</div>}>
        <span>kids</span>
      </PreErrored>,
    );
    expect(html).toContain('custom-fb');
    expect(html).toContain('custom');
    expect(html).not.toContain('Something went wrong');
    expect(html).not.toContain('Try again');
  });

  it('honours fallbackRender, passing the captured error and a reset handle', () => {
    class PreErrored extends ErrorBoundary {
      override state = { error: new Error('rendered via prop') };
    }
    const html = renderToStaticMarkup(
      <PreErrored
        fallbackRender={({ error, reset }) => {
          expect(typeof reset).toBe('function');
          return <div className="rp-fb">{error.message}</div>;
        }}
      >
        <span>kids</span>
      </PreErrored>,
    );
    expect(html).toContain('rp-fb');
    expect(html).toContain('rendered via prop');
  });
});
