// @vitest-environment jsdom
import { act, renderHook, waitFor } from '@testing-library/react';
import { beforeEach, describe, expect, it, vi } from 'vitest';

vi.mock('../../lib/api', () => ({
  api: { uploadComposerAttachment: vi.fn(async (_sid: string, f: File) => ({ path: `/att/${f.name}`, name: f.name, mime: f.type })) },
}));
vi.mock('../../lib/remoteLog', () => ({ remoteLog: { error: vi.fn() } }));

import { api } from '../../lib/api';
import { useComposerAttachments } from './useComposerAttachments';

const img = new File(['x'], 'shot.png', { type: 'image/png' });
const doc = new File(['y'], 'notes.txt', { type: 'text/plain' });

describe('useComposerAttachments', () => {
  beforeEach(() => vi.clearAllMocks());

  it('inlines images, uploads other files, and builds the reference text', async () => {
    const { result } = renderHook(() => useComposerAttachments({ current: 's1' }, false));
    await act(() => result.current.addFiles([img, doc]));
    await waitFor(() => expect(result.current.files).toHaveLength(1));
    expect(result.current.images[0].url).toMatch(/^data:image\/png/);
    expect(api.uploadComposerAttachment).toHaveBeenCalledWith('s1', doc);
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

    rerender({ disabled: false });
    act(() => result.current.handlePaste(paste));
    expect(preventDefault).toHaveBeenCalled();
    await waitFor(() => expect(result.current.images).toHaveLength(1));

    const drop = { preventDefault: vi.fn(), stopPropagation: vi.fn(), dataTransfer: { files: [img] } } as unknown as React.DragEvent;
    act(() => result.current.handleDrop(drop));
    await waitFor(() => expect(result.current.images).toHaveLength(2));
    act(() => result.current.handleDragOver(drop));
    expect(drop.preventDefault).toHaveBeenCalledTimes(2);
  });
});
