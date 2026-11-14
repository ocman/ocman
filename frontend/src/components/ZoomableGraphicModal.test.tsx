// @vitest-environment jsdom
import { fireEvent, render, screen } from '@testing-library/react';
import type { ReactNode } from 'react';
import { describe, expect, it, vi } from 'vitest';
import { ZoomableGraphicModal } from './ZoomableGraphicModal';

const controls = vi.hoisted(() => ({ zoomIn: vi.fn(), zoomOut: vi.fn(), centerView: vi.fn() }));

vi.mock('react-zoom-pan-pinch', () => ({
  TransformWrapper: ({ children, onTransform, ...props }: {
    children: (actions: typeof controls) => ReactNode;
    onTransform: (ref: unknown, state: { scale: number }) => void;
  }) => <div data-testid="transform" data-options={JSON.stringify(props)}>
    <button onClick={() => onTransform(null, { scale: 1.5 })}>Simulate transform</button>
    {children(controls)}
  </div>,
  TransformComponent: ({ children }: { children: ReactNode }) => <div>{children}</div>,
}));

describe('ZoomableGraphicModal', () => {
  it('allows free panning and keeps zoom, reset, navigation and dismissal controls', () => {
    const onClose = vi.fn();
    render(<ZoomableGraphicModal label="Diagram" closeLabel="Close diagram" onClose={onClose} navigation={<span>Gallery navigation</span>}>
      <svg aria-label="Graphic" />
    </ZoomableGraphicModal>);
    const options = JSON.parse(screen.getByTestId('transform').dataset.options!);
    expect(options).toMatchObject({ minScale: 0.25, maxScale: 4, centerOnInit: true, limitToBounds: false });
    expect(options.centerZoomedOut).toBeFalsy();
    expect(screen.getByLabelText('Graphic')).toBeInTheDocument();
    expect(screen.getByText('Gallery navigation')).toBeInTheDocument();
    fireEvent.click(screen.getByRole('button', { name: 'Zoom in' }));
    expect(controls.zoomIn).toHaveBeenCalledWith(0.25, 0);
    fireEvent.click(screen.getByRole('button', { name: 'Zoom out' }));
    expect(controls.zoomOut).toHaveBeenCalledWith(0.25, 0);
    fireEvent.click(screen.getByRole('button', { name: 'Simulate transform' }));
    expect(screen.getByText('150%')).toBeInTheDocument();
    fireEvent.click(screen.getByRole('button', { name: 'Reset view' }));
    expect(controls.centerView).toHaveBeenCalledWith(1, 0);
    fireEvent.click(screen.getByRole('button', { name: 'Close diagram' }));
    expect(onClose).toHaveBeenCalledOnce();
  });

  it('accepts an image-specific maximum zoom', () => {
    render(<ZoomableGraphicModal label="Image" closeLabel="Close image" maxScale={8} onClose={vi.fn()}>Image</ZoomableGraphicModal>);
    expect(JSON.parse(screen.getByTestId('transform').dataset.options!).maxScale).toBe(8);
  });
});
