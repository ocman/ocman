// @vitest-environment jsdom
import { act, renderHook, waitFor } from '@testing-library/react';
import { beforeEach, describe, expect, it, vi } from 'vitest';

vi.mock('../../lib/api', () => ({
  api: { uploadComposerAttachment: vi.fn(async (_sid: string, f: File) => ({ path: `/att/${f.name}`, name: f.name, mime: f.type })) },
}));
vi.mock('../../lib/remoteLog', () => ({ remoteLog: { error: vi.fn() } }));

import { api } from '../../lib/api';
import { useComposerAttachments } from './useComposerAttachments';
import { pendingAttachmentWriter, resetDraftPayloadsForTests, updateDraftAttachments, usePendingDraftPayload } from '../../lib/pendingDraftPayloads';
import { canAutomaticallyReload } from '../../lib/automaticReload';

const img = new File(['x'], 'shot.png', { type: 'image/png' });
const doc = new File(['y'], 'notes.txt', { type: 'text/plain' });

describe('useComposerAttachments', () => {
  beforeEach(() => { vi.clearAllMocks(); resetDraftPayloadsForTests(); });

  it('blocks synchronously before React commits and through upload completion and removal', async () => {
    let resolve!: (value: Awaited<ReturnType<typeof api.uploadComposerAttachment>>) => void;
    vi.mocked(api.uploadComposerAttachment).mockImplementationOnce(() => new Promise(done => { resolve = done; }));
    const { result } = renderHook(() => useComposerAttachments({ current: 'existing' }, false));
    let uploading!: Promise<void>;
    act(() => {
      uploading = result.current.addFiles([doc]);
      expect(canAutomaticallyReload()).toBe(false);
    });
    act(() => result.current.clear());
    expect(canAutomaticallyReload()).toBe(false);
    await act(async () => {
      resolve({ path: '/att/notes.txt', name: 'notes.txt', mime: 'text/plain', size: 1 });
      await uploading;
    });
    expect(canAutomaticallyReload()).toBe(false);
    act(() => result.current.removeFile(0));
    expect(canAutomaticallyReload()).toBe(true);
  });

  it('releases a failed read without leaving an automatic reload blocker', async () => {
    const read = vi.spyOn(FileReader.prototype, 'readAsDataURL').mockImplementation(function (this: FileReader) {
      this.dispatchEvent(new ProgressEvent('error'));
    });
    try {
      const { result } = renderHook(() => useComposerAttachments({ current: 'existing' }, false));
      await act(() => result.current.addFiles([img]));
      expect(result.current.pending).toBe(0);
      expect(canAutomaticallyReload()).toBe(true);
    } finally { read.mockRestore(); }
  });

  it('does not unregister controlled draft attachments when their composer unmounts', async () => {
    const id = 'controlled-lifetime';
    const { result, unmount } = renderHook(() => useComposerAttachments({ current: undefined }, false, undefined,
      usePendingDraftPayload(id), () => pendingAttachmentWriter(id), payload => updateDraftAttachments(id, payload)));
    await act(() => result.current.addFiles([img]));
    unmount();
    expect(canAutomaticallyReload()).toBe(false);
    updateDraftAttachments(id, { images: [], files: [] });
    expect(canAutomaticallyReload()).toBe(true);
  });

  it('removes controlled attachments from the authoritative draft without restoring old snapshots', async () => {
    const id = 'controlled-attachments';
    const { result, rerender } = renderHook(() => useComposerAttachments({ current: undefined }, false, undefined,
      usePendingDraftPayload(id), () => pendingAttachmentWriter(id), (payload) => updateDraftAttachments(id, payload)));
    await act(() => result.current.addFiles([img, doc]));
    expect(result.current.images).toHaveLength(1);
    expect(result.current.files).toHaveLength(1);
    act(() => result.current.removeImage(0));
    act(() => result.current.removeFile(0));
    rerender();
    expect(result.current.images).toEqual([]);
    expect(result.current.files).toEqual([]);
  });

  it('inlines images, uploads other files, and builds the reference text', async () => {
    const { result } = renderHook(() => useComposerAttachments({ current: 's1' }, false));
    await act(() => result.current.addFiles([img, doc]));
    await waitFor(() => expect(result.current.files).toHaveLength(1));
    expect(result.current.images[0].url).toMatch(/^data:image\/png/);
    expect(api.uploadComposerAttachment).toHaveBeenCalledWith('s1', doc, undefined);
    expect(result.current.fileReferenceText).toBe('Attached files saved on disk:\n- /att/notes.txt (text/plain)');

    act(() => result.current.removeImage(0));
    act(() => result.current.removeFile(0));
    expect(result.current.images).toEqual([]);
    expect(result.current.files).toEqual([]);
    expect(result.current.fileReferenceText).toBe('');
  });

  it('skips uploads without a session and clears everything', async () => {
    const { result } = renderHook(() => useComposerAttachments({ current: undefined }, false));
    await act(() => result.current.addFiles([img, doc]));
    expect(api.uploadComposerAttachment).not.toHaveBeenCalled();
    expect(result.current.images).toHaveLength(1);
    act(() => result.current.clear());
    expect(result.current.images).toEqual([]);
  });

  it('accepts pasted and dropped images unless disabled', async () => {
    const { result, rerender } = renderHook(
      ({ disabled }: { disabled: boolean }) => useComposerAttachments({ current: 's1' }, disabled),
      { initialProps: { disabled: true } },
    );
    const preventDefault = vi.fn();
    const paste = {
      preventDefault,
      clipboardData: { items: [{ type: 'image/png', getAsFile: () => img }] },
    } as unknown as React.ClipboardEvent<HTMLTextAreaElement>;
    act(() => result.current.handlePaste(paste));
    expect(preventDefault).not.toHaveBeenCalled();
    await act(() => result.current.addFiles([img, doc]));
    expect(result.current.pending).toBe(0);
    expect(result.current.images).toEqual([]);
    expect(api.uploadComposerAttachment).not.toHaveBeenCalled();

    rerender({ disabled: false });
    act(() => result.current.handlePaste(paste));
    expect(preventDefault).toHaveBeenCalled();
    expect(result.current.pending).toBe(1);
    await waitFor(() => expect(result.current.images).toHaveLength(1));
    expect(result.current.pending).toBe(0);

    const drop = { preventDefault: vi.fn(), stopPropagation: vi.fn(), dataTransfer: { files: [img] } } as unknown as React.DragEvent;
    act(() => result.current.handleDrop(drop));
    await waitFor(() => expect(result.current.images).toHaveLength(2));
    act(() => result.current.handleDragOver(drop));
    expect(drop.preventDefault).toHaveBeenCalledTimes(2);
  });
});
