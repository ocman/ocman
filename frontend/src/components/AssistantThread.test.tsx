// @vitest-environment jsdom
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { render } from '@testing-library/react';
import type { ReactNode } from 'react';

vi.mock('@assistant-ui/react', async () => {
  const React = await import('react');
  const Root = ({ children }: { children: ReactNode }) => <div>{children}</div>;
  const Viewport = React.forwardRef<HTMLDivElement, { children: ReactNode; className?: string }>(
    ({ children, className }, ref) => <div ref={ref} className={className}>{children}</div>,
  );
  return {
    ThreadPrimitive: {
      Root,
      Viewport,
      Empty: ({ children }: { children: ReactNode }) => <>{children}</>,
      Messages: () => null,
      ViewportFooter: React.forwardRef<HTMLDivElement, { children: ReactNode; className?: string }>(
        ({ children, className }, ref) => <div ref={ref} className={className}>{children}</div>,
      ),
      ScrollToBottom: ({ children }: { children: ReactNode }) => <button type="button">{children}</button>,
    },
    MessagePrimitive: {},
    useMessage: () => undefined,
  };
});

import { AssistantThread } from './AssistantThread';

class StubResizeObserver {
  observe() {}
  disconnect() {}
}

beforeEach(() => vi.stubGlobal('ResizeObserver', StubResizeObserver));
afterEach(() => vi.unstubAllGlobals());

describe('AssistantThread pagination', () => {
  it('does not load older messages when a thread first mounts at its tail', () => {
    const onLoadMore = vi.fn();

    render(<AssistantThread hasMore onLoadMore={onLoadMore} />);

    expect(onLoadMore).not.toHaveBeenCalled();
  });
});
