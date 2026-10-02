// @vitest-environment jsdom
import { act, fireEvent, render, screen, waitFor } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { MemoryRouter, Route, Routes, useParams, useLocation, useNavigate } from 'react-router-dom';
import { Composer } from './Composer';
import { useWorktreeSubmission } from './worktreeSubmission';

const originalCommand = vi.fn();
const originalShell = vi.fn();
let requests: { path: string; body: Record<string, unknown> }[];
let creation: Promise<Response> | undefined;
let executionFailure: boolean;
let execution: Promise<Response> | undefined;
const json = (value: unknown) => new Response(JSON.stringify(value), { headers: { 'Content-Type': 'application/json' } });

beforeEach(() => {
  vi.clearAllMocks();
  requests = [];
  creation = undefined;
  executionFailure = false;
  execution = undefined;
  localStorage.clear();
  useWorktreeSubmission.setState({ entries: {} });
  Element.prototype.scrollIntoView = vi.fn();
  vi.stubGlobal('fetch', vi.fn(async (input: string, init?: RequestInit) => {
    const path = String(input);
    if (init?.method === 'POST') {
      requests.push({ path, body: JSON.parse(String(init.body)) });
      if (path.startsWith('/api/worktree/create-and-launch')) {
        return creation ?? json({ sessionId: 'child', worktreePath: '/worktrees/fix', branch: 'fix-1234' });
      }
      if (executionFailure) return new Response('backend unavailable', { status: 503 });
      if (execution) return execution;
      return new Response(null, { status: 204 });
    }
    if (path.startsWith('/api/git/info')) return json({ '/repo': { branch: 'main', ahead: 0, behind: 0, dirty: false }, '/worktrees/fix': { branch: 'fix-1234' } });
    if (path.startsWith('/api/worktree/list')) return json({ worktrees: [{ path: '/repo', main: true, branch: 'main' }, { path: '/worktrees/fix', main: false, branch: 'fix-1234' }] });
    if (path.includes('/commands')) return json([{ name: 'implement', description: 'Implement a feature' }]);
    if (path.startsWith('/api/session/')) {
      const id = path.split('/')[3].split('?')[0];
      return json({ session: { id, directory: id === 'parent' ? '/repo' : '/worktrees/fix', remoteId: 'machine', platform: 'r-machine:opencode' } });
    }
    return json([]);
  }));
});
afterEach(() => vi.unstubAllGlobals());

function SessionComposer() {
  const { id } = useParams();
  const navigate = useNavigate();
  return <><output>{useLocation().pathname}</output><button onClick={() => navigate('/session/other')}>Switch session</button><Composer sessionId={id} directory={id === 'parent' ? '/repo' : '/worktrees/fix'} newConversation worktreesSupported
    isRunning={false} shellExec selectedModel="provider/big" selectedAgent="plan" selectedReasoning="high"
    onCommand={originalCommand} onShell={originalShell} onAbort={() => {}} /></>;
}

async function start() {
  render(<MemoryRouter initialEntries={['/session/parent']}><Routes>
    <Route path="/session/:id" element={<SessionComposer />} />
  </Routes></MemoryRouter>);
  await waitFor(() => expect(screen.getByRole('textbox')).toBeEnabled());
}

function submit(text: string) {
  fireEvent.input(screen.getByRole('textbox'), { target: { value: text } });
  fireEvent.keyDown(screen.getByRole('textbox'), { key: 'Enter' });
}

describe('composer worktree execution', () => {
  it('keeps the user on their newer route when workspace creation completes', async () => {
    let finish!: (response: Response) => void;
    creation = new Promise((resolve) => { finish = resolve; });
    await start();
    submit('/implement login');
    await waitFor(() => expect(requests).toHaveLength(1));
    fireEvent.click(screen.getByRole('button', { name: 'Switch session' }));
    expect(await screen.findByText('/session/other')).toBeInTheDocument();
    await act(async () => { finish(json({ sessionId: 'child', worktreePath: '/worktrees/fix', branch: 'fix-1234' })); });
    await waitFor(() => expect(requests).toHaveLength(2));
    expect(screen.getByText('/session/other')).toBeInTheDocument();
    expect(requests[1].path).toBe('/api/session/child/command?platform=r-machine%3Aopencode');
  });

  it.each(['/implement login', '!sleep 60', 'Fix login'])('opens the child while %s is still executing', async (text) => {
    let finish!: (response: Response) => void;
    execution = new Promise((resolve) => { finish = resolve; });
    await start();
    submit(text);
    await waitFor(() => expect(requests).toHaveLength(2));
    expect(await screen.findByText('/session/child')).toBeInTheDocument();
    expect(await screen.findByRole('button', { name: 'Stop generation' })).toBeInTheDocument();
    await act(async () => { finish(new Response(null, { status: 204 })); });
    await waitFor(() => expect(screen.getByRole('textbox')).toBeEnabled());
    expect(screen.getByRole('textbox')).toHaveValue('');
  });
  it.each([
    ['/implement login', 'command', { command: 'implement', arguments: 'login', model: 'provider/big', agent: 'plan', reasoning: 'high' }],
    ['!touch feature.txt', 'shell', { command: 'touch feature.txt', agent: 'plan' }],
  ])('routes %s to the new workspace, never to the original checkout', async (text, endpoint, body) => {
    await start();
    expect(screen.getByRole('combobox', { name: 'Session target' })).toHaveValue('worktree');
    submit(text);
    await waitFor(() => expect(requests).toContainEqual({ path: `/api/session/child/${endpoint}?platform=r-machine%3Aopencode`, body }));
    expect(requests[0].body.prompt).toBe(text);
    expect(originalCommand).not.toHaveBeenCalled();
    expect(originalShell).not.toHaveBeenCalled();
    expect(requests.some((request) => request.path.startsWith('/api/session/parent/'))).toBe(false);
  });

  it.each(['/implement login', '!touch feature.txt'])('retains %s while creating the workspace and after a creation failure', async (text) => {
    let finish!: (response: Response) => void;
    creation = new Promise((resolve) => { finish = resolve; });
    await start();
    submit(text);
    expect(screen.getByRole('textbox')).toHaveValue(text);
    expect(screen.getByRole('textbox')).toBeDisabled();
    await act(async () => { finish(new Response('workspace failed', { status: 409 })); });
    await waitFor(() => expect(screen.getByRole('textbox')).toBeEnabled());
    expect(screen.getByRole('textbox')).toHaveValue(text);
    expect(screen.getByRole('alert')).toHaveTextContent('workspace failed');
    expect(originalCommand).not.toHaveBeenCalled();
    expect(originalShell).not.toHaveBeenCalled();
  });

  it('keeps UI-only slash commands local without creating a worktree', async () => {
    await start();
    submit('/details ');
    await waitFor(() => expect(originalCommand).toHaveBeenCalledWith('details', ''));
    expect(requests).toEqual([]);
  });

  it.each(['/implement login', '!touch feature.txt'])('does not automatically replay %s after an uncertain execution failure', async (text) => {
    executionFailure = true;
    await start();
    submit(text);
    await waitFor(() => expect(screen.getByRole('alert')).toBeInTheDocument());
    await waitFor(() => expect(screen.getByRole('textbox')).toBeEnabled());
    expect(screen.getByText('/session/child')).toBeInTheDocument();
    expect(screen.getByRole('alert')).toHaveTextContent(text);
    expect(useWorktreeSubmission.getState().entries.child.text).toBe(text);
    expect(requests).toHaveLength(2);
    executionFailure = false;
    fireEvent.click(screen.getByRole('button', { name: 'Retry' }));
    await waitFor(() => expect(screen.queryByRole('alert')).not.toBeInTheDocument());
    expect(requests.filter((request) => request.path.startsWith('/api/worktree/create-and-launch'))).toHaveLength(1);
    expect(requests).toHaveLength(3);
  });
});
