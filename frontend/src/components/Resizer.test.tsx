// @vitest-environment jsdom
import { fireEvent, render, screen } from '@testing-library/react';
import { beforeAll, describe, expect, it, vi } from 'vitest';
import { Resizer } from './Resizer';

beforeAll(() => {
  // jsdom has no PointerEvent; a MouseEvent subclass carries clientX.
  if (!('PointerEvent' in window)) {
    class PointerEventShim extends MouseEvent {
      pointerId: number;
      pointerType: string;
      constructor(type: string, init: PointerEventInit = {}) {
        super(type, init);
        this.pointerId = init.pointerId ?? 1;
        this.pointerType = init.pointerType ?? 'mouse';
      }
    }
    Object.defineProperty(window, 'PointerEvent', { value: PointerEventShim, configurable: true });
  }
  HTMLElement.prototype.setPointerCapture = vi.fn();
  HTMLElement.prototype.releasePointerCapture = vi.fn();
});

function renderResizer(props: { mirrored?: boolean } = {}) {
  const setWidth = vi.fn();
  render(
    <aside data-testid="panel" style={{ width: 300 }}>
      <Resizer
        width={300}
        setWidth={setWidth}
        min={200}
        max={500}
        defaultWidth={280}
        ariaLabel="Resize"
        {...props}
      />
    </aside>,
  );
  return { setWidth, handle: screen.getByRole('separator'), panel: screen.getByTestId('panel') };
}

describe('Resizer', () => {
  it('previews the drag on the panel and commits once on pointerup', () => {
    const { setWidth, handle, panel } = renderResizer();
    fireEvent.pointerDown(handle, { clientX: 100, button: 0, pointerId: 1 });
    fireEvent.pointerMove(handle, { clientX: 140, pointerId: 1 });
    fireEvent.pointerMove(handle, { clientX: 150, pointerId: 1 });
    expect(setWidth).not.toHaveBeenCalled();
    expect(panel.style.width).toBe('350px');

    fireEvent.pointerUp(handle, { clientX: 150, pointerId: 1 });
    expect(setWidth).toHaveBeenCalledTimes(1);
    expect(setWidth).toHaveBeenCalledWith(350);
  });

  it('clamps the preview and inverts the delta when mirrored', () => {
    const { setWidth, handle, panel } = renderResizer({ mirrored: true });
    fireEvent.pointerDown(handle, { clientX: 500, button: 0, pointerId: 1 });
    fireEvent.pointerMove(handle, { clientX: 100, pointerId: 1 });
    expect(panel.style.width).toBe('500px');
    fireEvent.pointerUp(handle, { clientX: 100, pointerId: 1 });
    expect(setWidth).toHaveBeenCalledWith(500);
  });

  it('does not commit a click without movement', () => {
    const { setWidth, handle } = renderResizer();
    fireEvent.pointerDown(handle, { clientX: 100, button: 0, pointerId: 1 });
    fireEvent.pointerUp(handle, { clientX: 100, pointerId: 1 });
    expect(setWidth).not.toHaveBeenCalled();
  });

  it('keeps keyboard resizing immediate', () => {
    const { setWidth, handle } = renderResizer();
    fireEvent.keyDown(handle, { key: 'ArrowRight' });
    expect(setWidth).toHaveBeenCalledWith(316);
    fireEvent.keyDown(handle, { key: 'Home' });
    expect(setWidth).toHaveBeenCalledWith(200);
  });
});
