// @vitest-environment jsdom
import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { beforeEach, describe, expect, it, vi } from 'vitest';

class ResizeObserver {
  observe() {}
  disconnect() {}
  unobserve() {}
}
vi.stubGlobal('ResizeObserver', ResizeObserver);

const { initializeMermaid, renderDiagram } = vi.hoisted(() => ({
  initializeMermaid: vi.fn(),
  renderDiagram: vi.fn(),
}));
vi.mock('mermaid', () => ({
  default: {
    initialize: initializeMermaid,
    render: renderDiagram,
  },
}));

import { MarkdownText } from './MarkdownText';

describe('MarkdownText', () => {
  beforeEach(() => {
    renderDiagram.mockReset().mockResolvedValue({ svg: '<svg aria-label="diagram"></svg>' });
  });

  it('renders mermaid code blocks as diagrams', async () => {
    render(<MarkdownText text={'```mermaid\nflowchart LR\n  A --> B\n```'} />);

    await waitFor(() => expect(renderDiagram).toHaveBeenCalledWith(expect.any(String), 'flowchart LR\n  A --> B\n'));
    expect(initializeMermaid).toHaveBeenCalledWith(expect.objectContaining({
      theme: 'base',
      themeVariables: expect.objectContaining({
        darkMode: true,
        signalTextColor: '#cdd6f4',
        signalColor: '#a6adc8',
      }),
    }));
    expect(screen.getByLabelText('diagram')).toBeInTheDocument();
  });

  it('opens Mermaid diagrams in a zoomable modal', async () => {
    const user = userEvent.setup();
    render(<MarkdownText text={'```mermaid\nflowchart LR\n  A --> B\n```'} />);

    await user.click(await screen.findByRole('button', { name: 'Expand Mermaid diagram' }));
    const dialog = screen.getByRole('dialog', { name: 'Mermaid diagram' });
    const diagram = dialog.querySelector<HTMLElement>('.oc-mermaid-modal-diagram');

    expect(dialog.parentElement?.parentElement).toBe(document.body);
    expect(screen.getByLabelText('Mermaid diagram viewport')).toBeInTheDocument();
    expect(diagram).toBeInTheDocument();
    expect(screen.getByText('100%')).toBeInTheDocument();
    await user.click(screen.getByRole('button', { name: 'Zoom in' }));
    expect(screen.getByText('125%')).toBeInTheDocument();
    await user.click(screen.getByRole('button', { name: 'Zoom out' }));
    expect(screen.getByText('100%')).toBeInTheDocument();

    const viewport = screen.getByLabelText('Mermaid diagram viewport');
    fireEvent.touchStart(viewport, { touches: [{ clientX: 50, clientY: 100, pageX: 50, pageY: 100 }, { clientX: 150, clientY: 100, pageX: 150, pageY: 100 }] });
    fireEvent.touchMove(viewport, { touches: [{ clientX: 25, clientY: 100, pageX: 25, pageY: 100 }, { clientX: 175, clientY: 100, pageX: 175, pageY: 100 }] });
    expect(screen.getByText('180%')).toBeInTheDocument();

    await user.click(screen.getByRole('button', { name: 'Close diagram' }));
    expect(dialog).not.toBeInTheDocument();
  });

  it('keeps ordinary code blocks unchanged', () => {
    render(<MarkdownText text={'```ts\nconst answer = 42\n```'} />);

    expect(screen.getByTitle('Copy code')).toBeInTheDocument();
    expect(renderDiagram).not.toHaveBeenCalled();
  });

  it('shows the source when Mermaid cannot render it', async () => {
    renderDiagram.mockRejectedValueOnce(new Error('invalid diagram'));
    render(<MarkdownText text={'```mermaid\nnot a diagram\n```'} />);

    expect(await screen.findByText('not a diagram')).toBeInTheDocument();
  });
});
