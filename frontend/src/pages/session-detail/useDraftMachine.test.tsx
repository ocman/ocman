// @vitest-environment jsdom
import { act, renderHook, waitFor } from '@testing-library/react';
import { beforeEach, expect, it, vi } from 'vitest';
import { useDraftMachine } from './useDraftMachine';
import { getConversationDraft, hydrateDrafts, rememberConversationDraft, useNewConversationDrafts } from '../../lib/newConversationDrafts';
import { transact } from '../../lib/draftDb';
import type { NewSessionParams } from '../../lib/newSessionPath';

const params = { directory: '/source', remoteId: 'local', platform: 'opencode', draftId: 'clone-draft' };
const machine = { remoteId: 'box', remoteName: 'Build box', platform: 'r-box:opencode', dir: '/target/repo', cloneUrl: 'https://example.com/Org/Repo.git' };

beforeEach(() => { useNewConversationDrafts.setState({ drafts: [], starts: {}, saveErrors: {} }); });

it('keeps clone consent in the shared draft, not the route, and restores it after hydration', async () => {
  const navigate = vi.fn();
  const first = renderHook(() => useDraftMachine(params, { target: 'worktree', model: 'old/model' }, () => false, navigate));
  await act(() => first.result.current.onMachineChange(machine));
  expect(navigate).toHaveBeenCalledWith('/session/new?dir=%2Ftarget%2Frepo&remoteId=box&platform=r-box%3Aopencode&draftId=clone-draft');
  expect(getConversationDraft('clone-draft')).toMatchObject({ directory: machine.dir, remoteId: 'box', target: 'current', model: '', clone: { url: machine.cloneUrl, source: { directory: '/source', remoteId: 'local' } } });
  first.unmount();
  await transact(['drafts'], 'readonly', (tx) => tx.get('drafts', 'clone-draft'));
  useNewConversationDrafts.setState({ drafts: [] });
  await act(() => hydrateDrafts());
  const second = renderHook(() => useDraftMachine({ ...params, directory: machine.dir, remoteId: 'box', platform: machine.platform }, {}, () => false, navigate));
  expect(second.result.current.cloneURL).toBe(machine.cloneUrl);
  expect(second.result.current.machineSource).toEqual({ directory: '/source', remoteId: 'local' });
  const another = { ...machine, dir: '/another/repo', remoteId: 'other', platform: 'r-other:opencode' };
  await act(() => second.result.current.onMachineChange(another));
  expect(getConversationDraft('clone-draft')?.clone?.source).toEqual({ directory: '/source', remoteId: 'local' });
});

it('clears clone consent when switching to an existing checkout and resets a selected worktree', async () => {
  const navigate = vi.fn();
  const hook = renderHook(() => useDraftMachine(params, { target: 'dir:/source/worktree' }, () => false, navigate));
  await act(() => hook.result.current.onMachineChange({ ...machine, cloneUrl: undefined }));
  expect(getConversationDraft('clone-draft')).toMatchObject({ target: 'current', clone: undefined });
});

it('clears destination-specific consent on browser Back but retains it for platform changes', async () => {
  const navigate = vi.fn();
  const hook = renderHook(({ route }: { route: NewSessionParams }) => useDraftMachine(route, {}, () => false, navigate), { initialProps: { route: params } });
  await act(() => hook.result.current.onMachineChange(machine));
  const destination = { ...params, directory: machine.dir, remoteId: machine.remoteId, platform: machine.platform };
  hook.rerender({ route: destination });
  expect(hook.result.current.cloneURL).toBe(machine.cloneUrl);
  hook.rerender({ route: { ...destination, platform: 'r-box:claudecode' } });
  expect(hook.result.current.cloneURL).toBe(machine.cloneUrl);
  hook.rerender({ route: params });
  await waitFor(() => expect(getConversationDraft(params.draftId)?.directory).toBe(params.directory));
  expect(hook.result.current.cloneURL).toBeUndefined();
  expect(getConversationDraft(params.draftId)?.clone).toBeUndefined();
});

it('saves the submitted route when Back skips the machine-switch render', async () => {
  const hook = renderHook(() => useDraftMachine(params, { target: 'worktree' }, () => false, vi.fn()));
  await act(() => hook.result.current.onMachineChange(machine));
  // The router returned to the original location without committing the destination.
  act(() => hook.result.current.saveDraft());
  const stored = await transact(['drafts'], 'readonly', (tx) => tx.get('drafts', params.draftId));
  expect(stored).toMatchObject({ ...params, target: 'worktree', clone: undefined });
});

it('preserves peer edits made after this view changed machines', async () => {
  const hook = renderHook(() => useDraftMachine(params, {}, () => false, vi.fn()));
  await act(() => hook.result.current.onMachineChange(machine));
  act(() => rememberConversationDraft({ ...getConversationDraft(params.draftId)!, agent: 'plan' }));
  act(() => hook.result.current.saveDraft());
  const stored = await transact(['drafts'], 'readonly', (tx) => tx.get('drafts', params.draftId));
  expect(stored).toMatchObject({ directory: machine.dir, remoteId: machine.remoteId, agent: 'plan' });
});

it('keeps worktree selection for existing projects and refuses a locked or claimed draft', async () => {
  const navigate = vi.fn();
  let locked = true;
  const hook = renderHook(() => useDraftMachine(params, { target: 'worktree' }, () => locked, navigate));
  await expect(hook.result.current.onMachineChange(machine)).rejects.toThrow('already in progress');
  locked = false;
  useNewConversationDrafts.setState({ starts: { 'clone-draft': { version: 0, text: '' } } });
  await expect(hook.result.current.onMachineChange(machine)).rejects.toThrow('already in progress');
  useNewConversationDrafts.setState({ starts: {} });
  await act(() => hook.result.current.onMachineChange({ ...machine, cloneUrl: undefined }));
  await waitFor(() => expect(getConversationDraft('clone-draft')?.target).toBe('worktree'));
});
