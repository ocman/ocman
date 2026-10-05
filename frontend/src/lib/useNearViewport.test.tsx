// @vitest-environment jsdom
import { act, render, screen } from '@testing-library/react';
import { useRef } from 'react';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { useNearViewport } from './useNearViewport';

function Probe() {
  const ref = useRef<HTMLDivElement>(null);
  const near = useNearViewport(ref, '.scroller', 500);
  return <div ref={ref}>{near ? 'near' : 'far'}</div>;
}

afterEach(() => vi.unstubAllGlobals());

describe('useNearViewport', () => {
  it('is true immediately without IntersectionObserver', () => {
    vi.stubGlobal('IntersectionObserver', undefined);
    render(<Probe />);
    expect(screen.getByText('near')).toBeInTheDocument();
  });

  it('turns true once the element nears its scroll container, then stops observing', () => {
    let fire: (entries: { isIntersecting: boolean }[]) => void = () => {};
    const disconnect = vi.fn();
    const init: IntersectionObserverInit[] = [];
    vi.stubGlobal('IntersectionObserver', class {
      constructor(cb: typeof fire, opts: IntersectionObserverInit) { fire = cb; init.push(opts); }
      observe() {}
      disconnect = disconnect;
    });
    render(<div className="scroller" data-testid="scroller"><Probe /></div>);
    expect(screen.getByText('far')).toBeInTheDocument();
    expect(init[0]).toEqual({ root: screen.getByTestId('scroller'), rootMargin: '500px 0px' });
    act(() => fire([{ isIntersecting: false }]));
    expect(screen.getByText('far')).toBeInTheDocument();
    act(() => fire([{ isIntersecting: true }]));
    expect(screen.getByText('near')).toBeInTheDocument();
    expect(disconnect).toHaveBeenCalled();
  });
});
