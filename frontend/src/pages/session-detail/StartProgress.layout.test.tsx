// @vitest-environment jsdom
import { fireEvent, render, screen } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import { StartProgress } from './StartProgress';

vi.mock('../../lib/useCapabilities', () => ({ useCapabilities: () => undefined }));
vi.mock('../../components/AssistantThread', () => ({ ImageDisplay: () => null }));

describe('launch timeline', () => {
  it.each(['\n', '\r\n'])('preserves prompt line breaks and indentation with %j newlines', (newline) => {
    const text = ['First line', 'Second line', '', '  Indented line'].join(newline);
    render(<StartProgress prompt={text} steps={{ platform: 'active' }} />);
    const prompt = screen.getByTestId('pending-prompt');
    const textElement = screen.getByText('First line', { exact: false });
    expect(prompt.textContent).toBe(text);
    expect(getComputedStyle(textElement).whiteSpace).toBe('pre-wrap');
  });

  it('renders setup steps as child rows before their status icons', () => {
    render(<StartProgress prompt="First prompt" steps={{ platform: 'done', prompt: 'active' }} />);
    for (const id of ['platform', 'prompt']) {
      const step = screen.getByTestId(`start-step-${id}`);
      expect(step.firstElementChild?.textContent).toBe('└');
      expect(step.children[1].classList.contains('oc-launch-icon')).toBe(true);
    }
  });

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
