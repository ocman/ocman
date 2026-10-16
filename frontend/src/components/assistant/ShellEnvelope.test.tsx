// @vitest-environment jsdom
import { afterEach, describe, expect, it } from 'vitest';
import { fireEvent, render, screen } from '@testing-library/react';
import type { ComponentProps } from 'react';
import { ToolCallDisplay } from './ToolCallDisplay';
import { useUiStore } from '../../lib/uiStore';

type Props = ComponentProps<typeof ToolCallDisplay>;
const output = '(no output)\n\nCommand exceeded timeout of 120000 ms. Retry with a larger timeout if the command is expected to take longer.\n\nTimed out before completion';
const envelope = `<shell id="sh_test" state="completed" command="go test -cover ./... > tests.log 2>&1">\n${output}\n</shell>`;

describe('v2 shell envelopes', () => {
  afterEach(() => useUiStore.setState({ compactToolCalls: false }));

  it.each(['functions_shell', 'bash'])('renders %s results as a terminal without raw envelope metadata', (toolName) => {
    render(<ToolCallDisplay {...({ toolName, argsText: 'completed\n{"command":"go test"}', result: envelope } as Props)} />);
    const block = screen.getByTestId('shell-output-block');
    expect(block.textContent).toContain('$ go test -cover ./... > tests.log 2>&1');
    expect(block.textContent).toContain(output);
    expect(block.textContent).not.toContain('<shell');
    expect(block.textContent).not.toContain('sh_test');
    expect(block.textContent).not.toContain('</shell>');
  });

  it('keeps compact output expandable', () => {
    useUiStore.setState({ compactToolCalls: true });
    render(<ToolCallDisplay {...({ toolName: 'functions_shell', argsText: 'completed', result: envelope } as Props)} />);
    expect(screen.queryByTestId('shell-output-block')).not.toBeInTheDocument();
    fireEvent.click(screen.getByRole('button', { name: /^\$ go test/ }));
    expect(screen.getByTestId('shell-output-block')).toHaveTextContent('Timed out before completion');
    expect(screen.getByTestId('shell-output-block').textContent).toContain('$ go test -cover ./... > tests.log 2>&1');
    fireEvent.click(screen.getByRole('button', { name: /^\$ go test/ }));
    expect(screen.queryByTestId('shell-output-block')).not.toBeInTheDocument();
  });

  it('shows running state and renders HTML as literal output', () => {
    const result = '<shell id="sh_test" state="running" command="echo "hello"">\n<img src=x onerror=alert(1)>\n</shell>';
    const { container } = render(<ToolCallDisplay {...({ toolName: 'functions_shell', argsText: 'running', result } as Props)} />);
    expect(screen.getByTestId('bash-spinner')).toBeInTheDocument();
    expect(screen.getByTestId('shell-output-block').textContent).toContain('echo "hello"\n<img src=x onerror=alert(1)>');
    expect(container.querySelector('img')).toBeNull();
  });

  it('does not consume a shell example embedded in ordinary output', () => {
    const result = `Example:\n${envelope}`;
    render(<ToolCallDisplay {...({ toolName: 'bash', argsText: 'completed\ncat example.txt', result } as Props)} />);
    expect(screen.getByTestId('shell-output-block').textContent).toContain(result);
  });
});
