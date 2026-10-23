// @vitest-environment jsdom
import { afterEach, describe, expect, it } from 'vitest';
import { render, screen } from '@testing-library/react';
import type { ComponentProps } from 'react';
import { ToolCallDisplay } from './ToolCallDisplay';
import { V2Notification } from './V2Notification';
import { useUiStore } from '../../lib/uiStore';

type Props = ComponentProps<typeof ToolCallDisplay>;

describe('user quick shell output', () => {
  afterEach(() => useUiStore.setState({ compactToolCalls: false }));

  it.each(['running', 'completed', 'error'])('shows user-executed output while %s in compact mode', (status) => {
    useUiStore.setState({ compactToolCalls: true });
    render(<ToolCallDisplay {...({
      toolName: 'bash', argsText: `${status}\n@user-executed-tool\npwd`, result: '/workspace',
    } as Props)} />);
    expect(screen.getByTestId('shell-output-block')).toHaveTextContent('/workspace');
    expect(screen.getByText('The following tool was executed by the user')).toBeVisible();
  });

  it('shows user shell notifications in compact mode', () => {
    useUiStore.setState({ compactToolCalls: true });
    render(<V2Notification notification={{
      kind: 'shell', shell: { id: 'sh_user', status: 'completed', command: 'pwd', output: '/workspace', userExecuted: true },
    }} />);
    expect(screen.getByTestId('shell-output-block')).toHaveTextContent('/workspace');
  });

  it('shows user-executed shell envelopes in compact mode', () => {
    useUiStore.setState({ compactToolCalls: true });
    render(<ToolCallDisplay {...({
      toolName: 'bash', argsText: 'completed',
      result: 'The following shell command was executed by the user:\n<shell id="sh_user" state="completed" command="pwd">\n/workspace\n</shell>',
    } as Props)} />);
    expect(screen.getByTestId('shell-output-block')).toHaveTextContent('/workspace');
    expect(screen.getByText('The following tool was executed by the user')).toBeVisible();
  });
});
