// @vitest-environment jsdom
import { render, screen, waitFor } from '@testing-library/react';
import { beforeEach, describe, expect, it, vi } from 'vitest';

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
