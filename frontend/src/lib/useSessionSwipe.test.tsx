// @vitest-environment jsdom
import { beforeEach, afterEach, describe, expect, it, vi } from 'vitest';
import { fireEvent, render, screen } from '@testing-library/react';
import { useSessionSwipe } from './useSessionSwipe';

class TouchPointer extends MouseEvent {
  pointerId: number;
  pointerType: string;
  isPrimary: boolean;
  constructor(type: string, init: PointerEventInit = {}) {
    super(type, init);
    this.pointerId = init.pointerId ?? 1;
    this.pointerType = init.pointerType ?? 'touch';
    this.isPrimary = init.isPrimary ?? true;
  }
}

beforeEach(() => {
  vi.stubGlobal('PointerEvent', TouchPointer);
  vi.stubGlobal('matchMedia', () => ({ matches: true, addEventListener() {}, removeEventListener() {} }));
  HTMLElement.prototype.setPointerCapture = vi.fn();
});
afterEach(() => vi.unstubAllGlobals());

function setup(disabled = false) {
  const archive = vi.fn();
  const snooze = vi.fn();
  const navigate = vi.fn();
  function Row() {
    const swipe = useSessionSwipe({ disabled, onArchive: archive, onSnooze: snooze });
    return <div data-testid="row" {...swipe} onClick={navigate}><a href="#session">Session</a><button>Action</button></div>;
  }
  render(<Row />);
  const row = screen.getByTestId('row');
  function drag(x: number, y = 0, pointerType = 'touch', target = row) {
    fireEvent.pointerDown(target, { clientX: 150, clientY: 150, pointerId: 1, pointerType });
    fireEvent.pointerMove(row, { clientX: 150 + x, clientY: 150 + y, pointerId: 1, pointerType });
    fireEvent.pointerUp(row, { clientX: 150 + x, clientY: 150 + y, pointerId: 1, pointerType });
  }
  return { row, archive, snooze, navigate, drag };
}

describe('mobile session swipes', () => {
  it.each([[100, 'archive'], [-100, 'snooze']] as const)('drag %s invokes only %s and suppresses the following click', (distance, action) => {
    const ctx = setup();
    ctx.drag(distance);
    expect(ctx.archive).toHaveBeenCalledTimes(action === 'archive' ? 1 : 0);
    expect(ctx.snooze).toHaveBeenCalledTimes(action === 'snooze' ? 1 : 0);
    fireEvent.click(screen.getByRole('link'), { detail: 1 });
    expect(ctx.navigate).not.toHaveBeenCalled();
    expect(ctx.row.style.getPropertyValue('--session-swipe-offset')).toBe('0px');
  });

  it.each([[40, 0], [0, 120], [100, 150], [4, 0]])('does not act on short or vertical movement %s,%s', (x, y) => {
    const { drag, archive, snooze } = setup();
    drag(x, y);
    expect(archive).not.toHaveBeenCalled();
    expect(snooze).not.toHaveBeenCalled();
  });

  it('locks a vertical gesture to scrolling even when it moves sideways later', () => {
    const { row, archive } = setup();
    fireEvent.pointerDown(row, { clientX: 150, clientY: 150 });
    fireEvent.pointerMove(row, { clientX: 150, clientY: 180 });
    fireEvent.pointerMove(row, { clientX: 280, clientY: 180 });
    fireEvent.pointerUp(row, { clientX: 280, clientY: 180 });
    expect(archive).not.toHaveBeenCalled();
  });

  it('keeps dragging when pointer capture transfers from the row link to the row', () => {
    const { row, archive } = setup();
    const link = screen.getByRole('link');
    fireEvent.pointerDown(link, { clientX: 150, clientY: 150 });
    fireEvent.pointerMove(row, { clientX: 190, clientY: 150 });
    fireEvent.lostPointerCapture(link, { pointerId: 1 });
    fireEvent.pointerMove(row, { clientX: 250, clientY: 150 });
    fireEvent.pointerUp(row, { clientX: 250, clientY: 150 });
    expect(archive).toHaveBeenCalledTimes(1);
  });

  it.each(['pointerCancel', 'lostPointerCapture'] as const)('cancels without acting on %s', (event) => {
    const { row, archive, snooze } = setup();
    fireEvent.pointerDown(row, { clientX: 150, clientY: 150 });
    fireEvent.pointerMove(row, { clientX: 250, clientY: 150 });
    expect(row).toHaveAttribute('data-swipe-label', 'Archive');
    fireEvent[event](row, { pointerId: 1 });
    fireEvent.pointerUp(row, { clientX: 250, clientY: 150 });
    expect(archive).not.toHaveBeenCalled();
    expect(snooze).not.toHaveBeenCalled();
    expect(row).not.toHaveAttribute('data-swipe-label');
  });

  it('preserves taps, keyboard activation and a tap after a cancelled swipe', () => {
    const { row, drag, navigate } = setup();
    drag(0);
    fireEvent.click(row, { detail: 1 });
    drag(30);
    fireEvent.click(row, { detail: 0 });
    drag(0);
    fireEvent.click(row, { detail: 1 });
    expect(navigate).toHaveBeenCalledTimes(3);
  });

  it.each(['mouse', 'pen'])('ignores %s drags', (type) => {
    const { drag, archive } = setup();
    drag(120, 0, type);
    expect(archive).not.toHaveBeenCalled();
  });

  it('ignores drags on desktop, disabled rows and action buttons', () => {
    const { drag, archive } = setup(true);
    drag(120);
    expect(archive).not.toHaveBeenCalled();
  });

  it('ignores touch drags at desktop widths', () => {
    vi.stubGlobal('matchMedia', () => ({ matches: false, addEventListener() {}, removeEventListener() {} }));
    const { drag, archive } = setup();
    drag(120);
    expect(archive).not.toHaveBeenCalled();
  });

  it('does not steal gestures started on a control', () => {
    const { drag, archive } = setup();
    drag(120, 0, 'touch', screen.getByRole('button'));
    expect(archive).not.toHaveBeenCalled();
  });
});
