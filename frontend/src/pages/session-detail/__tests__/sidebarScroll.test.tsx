// @vitest-environment jsdom
import { fireEvent, screen, waitFor } from '@testing-library/react';
import { afterEach, beforeEach, expect, it, vi } from 'vitest';
import { makeSession, makeSessionDetail, renderSessionPage } from './harness';
import { useUiStore } from '../../../lib/uiStore';

beforeEach(() => {
  HTMLElement.prototype.scrollTo = vi.fn(function (this: HTMLElement, options: ScrollToOptions) {
    this.scrollTop = options.top ?? 0;
  }) as typeof HTMLElement.prototype.scrollTo;
  HTMLElement.prototype.scrollIntoView = vi.fn();
  vi.spyOn(HTMLElement.prototype, 'scrollHeight', 'get').mockReturnValue(2000);
  vi.spyOn(HTMLElement.prototype, 'clientHeight', 'get').mockReturnValue(500);
  useUiStore.setState({ sidebarView: 'recent' });
});

afterEach(() => vi.restoreAllMocks());

it.each(['current', 'other'])('scrolls to the bottom when selecting the %s sidebar session', async (target) => {
  const current = makeSession({ id: 'sess_1', title: 'Current session' });
  const other = makeSession({ id: 'sess_2', title: 'Other session' });
  const page = renderSessionPage({
    sessionId: current.id,
    detail: makeSessionDetail(current),
    sessions: [current, other],
    realAssistantThread: true,
    apiOverrides: { session: vi.fn(async (id: string) => makeSessionDetail(id === current.id ? current : other)) },
  });
  try {
    const viewport = await screen.findByTestId('conversation-viewport');
    fireEvent.wheel(viewport, { deltaY: -100 });
    viewport.scrollTop = 100;
    fireEvent.scroll(viewport);
    vi.mocked(HTMLElement.prototype.scrollTo).mockClear();

    fireEvent.click(await screen.findByText(target === 'current' ? 'Current session' : 'Other session'));

    await waitFor(() => expect(HTMLElement.prototype.scrollTo).toHaveBeenCalledWith({ top: 2000, behavior: 'auto' }));
    expect(screen.getByTestId('conversation-viewport').scrollTop).toBe(2000);
    expect(screen.queryByTestId('scroll-to-bottom')).not.toBeInTheDocument();
  } finally {
    page.result.unmount();
  }
});
