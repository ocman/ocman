// @vitest-environment jsdom
import { fireEvent, render, screen } from '@testing-library/react';
import { afterEach, expect, it, vi } from 'vitest';
import { PaneHeader } from './RightPanelPaneHeader';

const pluginTab = 'plugin:org.example.tree/items' as const;
function props() {
  return {
    tab: pluginTab,
    tabLabels: { session: 'Session changes', [pluginTab]: 'Items' },
    divider: true,
    summary: { files: 1, additions: 2, deletions: 1 },
    hasRefresh: true,
    loading: false,
    onRefreshClick: vi.fn(),
    hasFullscreen: true,
    onFullscreenClick: vi.fn(),
    resizeAboveIdx: 0,
    openTabs: ['session', pluginTab] as const,
    sizes: [0.4, 0.6],
    onResize: vi.fn(),
  };
}
afterEach(() => { vi.restoreAllMocks(); vi.unstubAllGlobals(); });

it('labels and keyboard-resizes a plugin split with bounded fractions', () => {
  const p = props();
  const { rerender } = render(<PaneHeader {...p} openTabs={[...p.openTabs]} />);
  const separator = screen.getByRole('separator', { name: 'Resize Session changes / Items' });
  expect(separator).toHaveAttribute('aria-valuenow', '40');
  for (const [key, before, after] of [
    ['ArrowDown', 0.44, 0.56], ['ArrowUp', 0.36, 0.64],
    ['PageDown', 0.5, 0.5], ['PageUp', 0.3, 0.7],
  ] as const) {
    fireEvent.keyDown(separator, { key });
    const call = p.onResize.mock.lastCall!;
    expect(call[0]).toBe(0);
    expect(call[1]).toBeCloseTo(before);
    expect(call[2]).toBeCloseTo(after);
  }
  fireEvent.keyDown(separator, { key: 'Enter' });
  expect(p.onResize).toHaveBeenCalledTimes(4);
  rerender(<PaneHeader {...p} openTabs={[...p.openTabs]} sizes={[0.1, 0.9]} />);
  fireEvent.keyDown(separator, { key: 'PageUp' });
  expect(p.onResize).toHaveBeenLastCalledWith(0, 0.1, 0.9);
});

it('pointer-resizes plugin splits and cleans up the drag on release', () => {
  vi.stubGlobal('PointerEvent', MouseEvent);
  vi.spyOn(HTMLElement.prototype, 'getBoundingClientRect').mockReturnValue({ height: 1000 } as DOMRect);
  const p = props();
  render(<div><PaneHeader {...p} openTabs={[...p.openTabs]} /></div>);
  fireEvent.pointerDown(screen.getByRole('separator'), { clientY: 500 });
  expect(document.body).toHaveClass('oc-sidebar-resizing');
  fireEvent.pointerMove(window, { clientY: 600 });
  expect(p.onResize).toHaveBeenLastCalledWith(0, 0.5, 0.5);
  fireEvent.pointerMove(window, { clientY: 2000 });
  expect(p.onResize).toHaveBeenLastCalledWith(0, 0.9, expect.closeTo(0.1));
  fireEvent.pointerUp(window);
  expect(document.body).not.toHaveClass('oc-sidebar-resizing');
});

it('keeps header controls interactive without starting a resize', () => {
  vi.stubGlobal('PointerEvent', MouseEvent);
  const p = props();
  render(<PaneHeader {...p} openTabs={[...p.openTabs]} />);
  const refresh = screen.getByRole('button', { name: 'Refresh' });
  fireEvent.pointerDown(refresh, { clientY: 100 });
  expect(document.body).not.toHaveClass('oc-sidebar-resizing');
  fireEvent.click(refresh);
  fireEvent.click(screen.getByRole('button', { name: 'Fullscreen' }));
  expect(p.onRefreshClick).toHaveBeenCalledOnce();
  expect(p.onFullscreenClick).toHaveBeenCalledOnce();
  expect(p.onResize).not.toHaveBeenCalled();
  expect(screen.getByText('1 file')).toBeInTheDocument();
});

it('renders a non-resizable single pane without diff or refresh controls', () => {
  const p = props();
  render(<PaneHeader {...p} openTabs={[pluginTab]} resizeAboveIdx={null} hasRefresh={false} hasFullscreen={false} summary={{ files: 0, additions: 0, deletions: 0 }} />);
  expect(screen.getByText('Items')).toBeInTheDocument();
  expect(screen.queryByRole('separator')).not.toBeInTheDocument();
  expect(screen.queryByRole('button')).not.toBeInTheDocument();
});
