// @vitest-environment jsdom
import { fireEvent, render, screen } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import { StartProgress } from './StartProgress';

vi.mock('../../lib/useCapabilities', () => ({ useCapabilities: () => undefined }));
vi.mock('../../components/AssistantThread', () => ({ ImageDisplay: () => null }));

describe('launch timeline', () => {
  it('puts the timestamp before the prompt and groups setup below it', () => {
    render(<StartProgress prompt="First prompt" steps={{ platform: 'active' }} />);
    const prompt = screen.getByTestId('pending-prompt');
    const timestamp = screen.getByTestId('launch-timestamp');
    const setup = screen.getByText('Setup ocman session').closest('details')!;
    expect(timestamp.compareDocumentPosition(prompt) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();
    expect(prompt.compareDocumentPosition(setup) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();
    expect(setup.open).toBe(true);
  });

  it('collapses completed setup and allows reopening it', () => {
    const view = render(<StartProgress prompt="First prompt" steps={{ prompt: 'active' }} />);
    view.rerender(<StartProgress prompt="First prompt" steps={{ prompt: 'done' }} />);
    const setup = screen.getByText('Setup ocman session').closest('details')!;
    expect(setup.open).toBe(false);
    fireEvent.click(screen.getByText('Setup ocman session'));
    expect(setup.open).toBe(true);
  });

  it('keeps failed setup expanded', () => {
    render(<StartProgress prompt="First prompt" steps={{ platform: 'error' }} />);
    expect(screen.getByText('Setup ocman session').closest('details')!.open).toBe(true);
  });
});
