// @vitest-environment jsdom
import { render, screen } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import type { WorkflowDefinition } from '../lib/api';
import { WorkflowBuilder } from './WorkflowBuilder';

vi.mock('../lib/api', () => ({ api: { models: vi.fn().mockResolvedValue([]) } }));

class ResizeObserver {
  observe() {}
  unobserve() {}
  disconnect() {}
}

vi.stubGlobal('ResizeObserver', ResizeObserver);

describe('WorkflowBuilder', () => {
  it('lays dependency targets below their sources regardless of definition order', () => {
    const definition: WorkflowDefinition = {
      id: 'release',
      name: 'Release',
      version: '1',
      concurrency: 1,
      directory: '/repo',
      triggers: [],
      nodes: [
        { id: 'ship', name: 'Ship', type: 'approval' },
        { id: 'review', name: 'Review', type: 'approval' },
      ],
      dependencies: [{ from: 'review', to: 'ship' }],
    };

    render(<WorkflowBuilder definition={definition} source="" onChange={vi.fn()} onSourceChange={vi.fn()} />);

    const review = screen.getByText('review').closest('.react-flow__node') as HTMLElement;
    const ship = screen.getByText('ship').closest('.react-flow__node') as HTMLElement;
    const y = (node: HTMLElement) => Number(/translate\([^,]+,\s*([\d.-]+)px\)/.exec(node.style.transform)?.[1]);
    expect(y(review)).toBeLessThan(y(ship));
  });
});
