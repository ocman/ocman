// @vitest-environment jsdom
import { useRef } from 'react';
import { render } from '@testing-library/react';
import { afterEach, expect, it, vi } from 'vitest';
import { useSidebarReorder } from './useSidebarReorder';

function List({ ids, view = 'recent' }: { ids: string[]; view?: string }) {
  const ref = useRef<HTMLDivElement>(null);
  useSidebarReorder(ref, view);
  return <div ref={ref}>{ids.map((id) => <div key={id} data-session-key={id}>{id}</div>)}</div>;
}

afterEach(() => { vi.restoreAllMocks(); vi.unstubAllGlobals(); });

function setup(reduced = false) {
  vi.stubGlobal('matchMedia', vi.fn(() => ({ matches: reduced })));
  vi.spyOn(HTMLElement.prototype, 'getBoundingClientRect').mockImplementation(function (this: HTMLElement) {
    const index = this.dataset.sessionKey ? [...this.parentElement!.children].indexOf(this) : 0;
    return { top: index * 40 } as DOMRect;
  });
  const cancel = vi.fn();
  const animate = vi.fn(() => ({ cancel }));
  Object.defineProperty(HTMLElement.prototype, 'animate', { configurable: true, value: animate });
  return { animate, cancel };
}

it('animates moved rows, leaves token-only updates alone, and cancels on unmount', () => {
  const { animate, cancel } = setup();
  const { rerender, unmount } = render(<List ids={['a', 'b']} />);
  expect(animate).not.toHaveBeenCalled();
  rerender(<List ids={['b', 'a']} />);
  expect(animate).toHaveBeenCalledTimes(2);
  expect(animate.mock.calls[0]).toEqual([
    [{ transform: 'translateY(40px)' }, { transform: 'translateY(0)' }],
    { duration: 180, easing: 'ease-out' },
  ]);
  rerender(<List ids={['b', 'a']} />);
  expect(animate).toHaveBeenCalledTimes(2);
  unmount();
  expect(cancel).toHaveBeenCalledTimes(2);
});

it('respects reduced motion and does not animate view switches', () => {
  const { animate } = setup(true);
  const { rerender } = render(<List ids={['a', 'b']} />);
  rerender(<List ids={['b', 'a']} />);
  expect(animate).not.toHaveBeenCalled();
  vi.stubGlobal('matchMedia', vi.fn(() => ({ matches: false })));
  rerender(<List ids={['a', 'b']} view="projects" />);
  expect(animate).not.toHaveBeenCalled();
});
