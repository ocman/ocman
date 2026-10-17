// @vitest-environment jsdom
import { render, screen } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import { StartProgress } from './StartProgress';

vi.mock('../../lib/useCapabilities', () => ({
  useCapabilities: () => ({ platforms: [
    { id: 'opencode', displayName: 'OpenCode' },
    { id: 'r-machine:claudecode', displayName: 'Claude Code' },
  ] }),
}));
vi.mock('../../components/AssistantThread', () => ({ ImageDisplay: () => null }));

describe('StartProgress platform labels', () => {
  it.each([
    ['opencode', 'Launching OpenCode', 'OpenCode ready', 'OpenCode failed to launch'],
    ['r-machine:claudecode', 'Launching Claude Code', 'Claude Code ready', 'Claude Code failed to launch'],
    ['unknown', 'Launching platform', 'Platform ready', 'Platform failed to launch'],
  ])('names the runtime for %s without conflating session creation', (platform, active, done, error) => {
    const view = render(<StartProgress platform={platform} prompt="Hello" steps={{ opencode: 'active', session: 'active' }} />);
    expect(screen.getByTestId('start-step-opencode')).toHaveTextContent(active);
    expect(screen.getByTestId('start-step-session')).toHaveTextContent('Creating session');
    view.rerender(<StartProgress platform={platform} prompt="Hello" steps={{ opencode: 'done' }} />);
    expect(screen.getByTestId('start-step-opencode')).toHaveTextContent(done);
    view.rerender(<StartProgress platform={platform} prompt="Hello" steps={{ opencode: 'error' }} />);
    expect(screen.getByTestId('start-step-opencode')).toHaveTextContent(error);
  });
});
