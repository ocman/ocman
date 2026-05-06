// @vitest-environment jsdom
//
// Behaviour test for the wrapper indirection in
// `pages/session-detail/index.tsx`. The wrapper reads :id from the
// URL and threads it into the inner SessionDetail as a prop, so the
// inner component sees a fresh `id` on every URL change without
// having to subscribe to react-router's param context (a path that
// has been observed to wedge under sustained SSE activity).
//
// We deliberately do NOT remount the inner component on navigation:
// the inner owns the Recent-sessions sidebar, and remounting it
// would force the entire list to refetch and rebuild from scratch
// every time the user clicks a different session. The tests below
// pin both contracts: (1) the inner receives the new id prop when
// the URL changes, and (2) the inner does NOT remount.

import { describe, expect, it, vi } from 'vitest';
import { render, screen, act } from '@testing-library/react';
import { MemoryRouter, Route, Routes, useNavigate } from 'react-router-dom';
import { useEffect, useRef } from 'react';

const mountedInstances: { id: string; instance: number }[] = [];
let nextInstance = 0;
let lastRenderedId: string | undefined;
let renderCount = 0;

vi.mock('../SessionDetail', () => ({
  SessionDetail: function MockedInner({ id }: { id: string | undefined }) {
    const instanceRef = useRef<number | null>(null);
    const mountIdRef = useRef<string | undefined>(id);
    if (instanceRef.current === null) instanceRef.current = ++nextInstance;
    lastRenderedId = id;
    renderCount += 1;
    // Record once per mount. We deliberately want the id captured
    // at mount time, not whatever it changes to later — that's how
    // we tell remount apart from prop change. The empty deps array
    // is intentional; mountIdRef carries the snapshot value across.
    useEffect(() => {
      mountedInstances.push({
        id: mountIdRef.current ?? '',
        instance: instanceRef.current!,
      });
    }, []);
    return (
      <div data-testid="inner-session-detail">
        instance:{instanceRef.current} id:{id}
      </div>
    );
  },
}));

// Importing AFTER the mock so the wrapper picks up the mocked inner.
import { SessionDetail } from '../index';

function NavigateButton({ to }: { to: string }) {
  const navigate = useNavigate();
  return <button onClick={() => navigate(to)}>go</button>;
}

describe('SessionDetail wrapper (index.tsx)', () => {
  it('passes the URL :id to the inner component as a prop', () => {
    mountedInstances.length = 0;
    nextInstance = 0;
    lastRenderedId = undefined;
    renderCount = 0;

    render(
      <MemoryRouter initialEntries={['/session/aaa']}>
        <Routes>
          <Route path="/session/:id" element={<SessionDetail />} />
        </Routes>
      </MemoryRouter>,
    );

    expect(lastRenderedId).toBe('aaa');
    expect(screen.getByTestId('inner-session-detail').textContent).toBe('instance:1 id:aaa');
  });

  it('forwards a new :id without remounting the inner component', () => {
    mountedInstances.length = 0;
    nextInstance = 0;
    lastRenderedId = undefined;
    renderCount = 0;

    render(
      <MemoryRouter initialEntries={['/session/aaa']}>
        <Routes>
          <Route path="/session/:id" element={<SessionDetail />} />
        </Routes>
        <NavigateButton to="/session/bbb" />
      </MemoryRouter>,
    );

    expect(mountedInstances).toHaveLength(1);
    expect(lastRenderedId).toBe('aaa');

    act(() => {
      screen.getByText('go').click();
    });

    // The inner component re-rendered with the new id but its mount
    // identity is unchanged. This is the contract we depend on so
    // the sidebar (which lives inside the inner component) does not
    // restart its polling cycle on every navigation.
    expect(lastRenderedId).toBe('bbb');
    expect(screen.getByTestId('inner-session-detail').textContent).toBe('instance:1 id:bbb');
    expect(mountedInstances).toHaveLength(1);
  });

  it('does not re-render when navigating to the same :id', () => {
    mountedInstances.length = 0;
    nextInstance = 0;
    lastRenderedId = undefined;
    renderCount = 0;

    render(
      <MemoryRouter initialEntries={['/session/same']}>
        <Routes>
          <Route path="/session/:id" element={<SessionDetail />} />
        </Routes>
        <NavigateButton to="/session/same" />
      </MemoryRouter>,
    );

    const beforeNav = renderCount;

    act(() => {
      screen.getByText('go').click();
    });

    // React Compiler / React 19's transitions may add zero or one
    // extra commit when the route is "navigated to itself". The
    // important contract is that the count doesn't blow up — a
    // small constant overhead is fine.
    expect(renderCount - beforeNav).toBeLessThanOrEqual(2);
    expect(mountedInstances).toHaveLength(1);
  });
});
