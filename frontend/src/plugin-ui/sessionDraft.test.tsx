// @vitest-environment jsdom
import { act, render, screen, waitFor } from '@testing-library/react';
import { MemoryRouter, Route, Routes, useLocation } from 'react-router-dom';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { beforeEach, expect, it, vi } from 'vitest';
import { useLayoutEffect } from 'react';
import type { SessionDraftOptions } from '@ocman/plugin-ui';
import { api } from '../lib/api';
import { getDraft, resetDraftTextsForTests, saveDraft } from '../lib/composerDraft';
import { getConversationDraft, rememberConversationDraft, useNewConversationDrafts } from '../lib/newConversationDrafts';
import { parseNewSessionParams } from '../lib/newSessionPath';
import { randomId } from '../lib/randomId';
import { PluginHostContext, type PluginHostValue } from './context';
import { useSessionDraft } from './index';
import { SESSION_DRAFT_LIMITS, validateSessionDraft } from './sessionDraft';

vi.mock('../lib/remoteLog', () => ({ remoteLog: { error: vi.fn(), warn: vi.fn() } }));
vi.mock('../lib/randomId', async (original) => ({ randomId: vi.fn((await original<typeof import('../lib/randomId')>()).randomId) }));

const projects = [
  { directory: '/work/service', sessionCount: 0 },
  { directory: '/work/handbook', remoteId: 'local', sessionCount: 0 },
  { directory: '/work/elsewhere', remoteId: 'build-box', sessionCount: 0 },
];
const host: PluginHostValue = { pluginId: 'org.example.tasks', pluginLabel: 'Tasks', ownerId: 'local', basePath: '/plugins/org.example.tasks', runAction: () => Promise.resolve() };
// Two unrelated workflows a plugin might hand to the user.
const maintenance: SessionDraftOptions = { directory: '/work/service', title: 'Update deprecated API calls',
  prompt: 'Replace calls to the deprecated client.fetchAll() with client.list() and keep the tests passing.' };
const docs: SessionDraftOptions = { directory: '/work/handbook', title: 'Refresh the setup guide',
  prompt: 'Update docs/setup.md for the new install command.\n\n> Reference text copied from a ticket:\n> <script>alert(1)</script>' };

const probe = {} as { handle: ReturnType<typeof useSessionDraft>; location: string };

function Opener() {
  const handle = useSessionDraft();
  useLayoutEffect(() => { probe.handle = handle; });
  return handle.error ? <p role="alert">{handle.error}</p> : null;
}

function Where() {
  const current = useLocation();
  useLayoutEffect(() => { probe.location = current.pathname + current.search; });
  return null;
}

function mount() {
  return render(<QueryClientProvider client={new QueryClient()}><MemoryRouter initialEntries={['/plugins/org.example.tasks']}>
    <PluginHostContext.Provider value={host}>
      <Routes><Route path="/plugins/:pluginId/*" element={<Opener />} /><Route path="*" element={null} /></Routes>
      <Where />
    </PluginHostContext.Provider>
  </MemoryRouter></QueryClientProvider>);
}

const params = () => parseNewSessionParams(new URL(probe.location, 'http://ocman').searchParams)!;

beforeEach(() => {
  resetDraftTextsForTests();
  useNewConversationDrafts.setState({ drafts: [], starts: {}, saveErrors: {} });
  vi.spyOn(api, 'projects').mockResolvedValue(projects as never);
});

it('opens editable drafts for unrelated workflows without starting anything', async () => {
  const start = vi.spyOn(api, 'startSession');
  rememberConversationDraft({ draftId: 'mine', directory: '/work/service', remoteId: 'local' });
  saveDraft('mine', 'my unsent text');
  mount();

  await act(() => probe.handle.open(maintenance));
  expect(probe.location).toMatch(/^\/session\/new\?/);
  const first = params();
  expect(first).toMatchObject({ directory: '/work/service', remoteId: 'local', title: 'Update deprecated API calls' });
  expect(getDraft(first.draftId!)).toBe(maintenance.prompt);
  expect(getConversationDraft(first.draftId!)).toMatchObject({ directory: '/work/service', remoteId: 'local' });

  await act(() => probe.handle.open(docs));
  const second = params();
  expect(second.draftId).not.toBe(first.draftId);
  // Reference text stays inert composer text.
  expect(getDraft(second.draftId!)).toBe(docs.prompt);
  expect(getDraft(first.draftId!)).toBe(maintenance.prompt);
  expect(getDraft('mine')).toBe('my unsent text');
  expect(useNewConversationDrafts.getState().drafts.map((d) => d.draftId).sort()).toEqual(['mine', first.draftId, second.draftId].sort());
  expect(useNewConversationDrafts.getState().starts).toEqual({});
  expect(start).not.toHaveBeenCalled();
  expect(probe.handle.error).toBeNull();
  expect(probe.handle.pending).toBe(false);
});

it('opens a draft without a title or prompt', async () => {
  mount();
  await act(() => probe.handle.open({ directory: '/work/service' }));
  expect(params()).toMatchObject({ directory: '/work/service', title: undefined });
  expect(getDraft(params().draftId!)).toBe('');
});

it.each([
  ['an unknown directory', { directory: '/work/unknown' }, '/work/unknown is not a project on this machine.'],
  ['another owner\'s project', { directory: '/work/elsewhere' }, '/work/elsewhere is not a project on this machine.'],
  ['a command prompt', { directory: '/work/service', prompt: '  /deploy now' }, 'cannot start with "/" or "!"'],
  ['a shell prompt', { directory: '/work/service', prompt: '!rm -rf build' }, 'cannot start with "/" or "!"'],
])('refuses %s visibly and stores nothing', async (_name, options, message) => {
  mount();
  await act(() => expect(probe.handle.open(options)).rejects.toThrow(message));
  expect(screen.getByRole('alert')).toHaveTextContent(message);
  expect(probe.location).toBe('/plugins/org.example.tasks');
  expect(useNewConversationDrafts.getState().drafts).toEqual([]);
});

it('reports a project catalog failure and clears the error on the next attempt', async () => {
  vi.mocked(api.projects).mockRejectedValueOnce(new Error('offline'));
  mount();
  await act(() => expect(probe.handle.open(maintenance)).rejects.toThrow('Could not load projects'));
  expect(screen.getByRole('alert')).toHaveTextContent('Could not load projects');
  await act(() => probe.handle.open(maintenance));
  expect(screen.queryByRole('alert')).toBeNull();
  expect(probe.location).toMatch(/^\/session\/new\?/);
});

it('never overwrites an existing draft identity', async () => {
  rememberConversationDraft({ draftId: 'fixed', directory: '/work/service', remoteId: 'local' });
  saveDraft('fixed', 'keep me');
  vi.mocked(randomId).mockReturnValueOnce('fixed');
  mount();
  await act(() => expect(probe.handle.open(maintenance)).rejects.toThrow('Could not save the draft: This draft already exists.'));
  expect(getDraft('fixed')).toBe('keep me');
  expect(probe.location).toBe('/plugins/org.example.tasks');
});

it('refuses a second open while one is pending', async () => {
  let release!: (value: never) => void;
  vi.mocked(api.projects).mockReturnValueOnce(new Promise((resolve) => { release = resolve; }));
  mount();
  const first = probe.handle.open(maintenance);
  await waitFor(() => expect(probe.handle.pending).toBe(true));
  await act(() => expect(probe.handle.open(docs)).rejects.toThrow('already opening'));
  await act(async () => { release(projects as never); await first; });
  expect(params().title).toBe('Update deprecated API calls');
});

it.each([
  ['missing options', undefined, 'options are missing'],
  ['no directory', { directory: '' }, 'needs a project directory'],
  ['a non-text directory', { directory: 7 }, 'directory must be text'],
  ['a control character in the directory', { directory: '/work/a\nb' }, 'needs a project directory'],
  ['a multi-line title', { directory: '/work/service', title: 'One\nTwo' }, 'one line of plain text'],
  ['a line separator in the title', { directory: '/work/service', title: 'One\u2028Two' }, 'one line of plain text'],
  ['a direction override in the title', { directory: '/work/service', title: 'Safe \u202etxt.exe' }, 'one line of plain text'],
  ['a long title', { directory: '/work/service', title: 'x'.repeat(SESSION_DRAFT_LIMITS.title + 1) }, 'title is longer than 200'],
  ['a long prompt', { directory: '/work/service', prompt: 'x'.repeat(SESSION_DRAFT_LIMITS.prompt + 1) }, 'prompt is longer than 65,536'],
  ['a non-text prompt', { directory: '/work/service', prompt: { text: 'hi' } }, 'prompt must be text'],
])('validates %s', (_name, options, message) => {
  expect(() => validateSessionDraft(options as never)).toThrow(message);
});

it('trims the title and keeps the prompt exactly', () => {
  expect(validateSessionDraft({ directory: '/work/service', title: '  Tidy  ', prompt: '  keep\n' }))
    .toEqual({ directory: '/work/service', title: 'Tidy', prompt: '  keep\n' });
});

it('needs a mounted plugin', () => {
  vi.spyOn(console, 'error').mockImplementation(() => {});
  function Orphan() { useSessionDraft(); return null; }
  expect(() => render(<MemoryRouter><QueryClientProvider client={new QueryClient()}><Orphan /></QueryClientProvider></MemoryRouter>)).toThrow(/only available inside a mounted plugin UI/);
});
