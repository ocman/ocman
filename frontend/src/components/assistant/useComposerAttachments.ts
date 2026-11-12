import { useCallback, useLayoutEffect, useRef, useState } from 'react';
import type { MutableRefObject } from 'react';
import { api } from '../../lib/api';
import { remoteLog } from '../../lib/remoteLog';
import { registerAutomaticReloadGuard } from '../../lib/automaticReloadGuards';

export interface AttachedImage {
  url: string;
  mime: string;
}

export interface AttachedFileRef {
  path: string;
  name: string;
  mime: string;
  /** No session exists yet; retain the browser file until the first start. */
  file?: File;
}

type AttachmentState = { images: AttachedImage[]; files: AttachedFileRef[]; pending: number };

function readFileAsDataURL(file: File): Promise<string> {
  return new Promise((resolve, reject) => {
    const reader = new FileReader();
    reader.onload = () => resolve(reader.result as string);
    reader.onerror = reject;
    reader.readAsDataURL(file);
  });
}

/**
 * Composer attachments: images are inlined as data URLs and sent with
 * the prompt; other files are uploaded to the session's attachment dir
 * and referenced by path in the prompt text.
 */
export function useComposerAttachments(sessionIdRef: MutableRefObject<string | undefined>, disabled: boolean | undefined, platform?: string,
  initial?: { images: AttachedImage[]; files: AttachedFileRef[]; pending?: number },
  onProcessing?: () => (batch: { images: AttachedImage[]; files: AttachedFileRef[] }) => void,
  onChange?: (payload: { images: AttachedImage[]; files: AttachedFileRef[] }) => void) {
  const [local, setLocal] = useState<AttachmentState>(() => ({ images: initial?.images || [], files: initial?.files || [], pending: 0 }));
  const live = useRef(local);
  // Update the guard's snapshot before React commits, including before the first
  // FileReader/upload await. Never put these side effects in a state updater.
  const updateLocal = useCallback((update: (state: AttachmentState) => AttachmentState) => {
    live.current = update(live.current);
    setLocal(live.current);
  }, []);
  const controlled = !!onProcessing && !!onChange;
  const images = controlled ? initial?.images || [] : local.images;
  const files = controlled ? initial?.files || [] : local.files;
  const pending = controlled ? initial?.pending || 0 : local.pending;
  // Controlled drafts retain their existing store ownership and lifetime.
  useLayoutEffect(() => registerAutomaticReloadGuard(() => !controlled && Boolean(
    live.current.images.length || live.current.files.length || live.current.pending)), [controlled]);

  const addFiles = useCallback(async (all: File[]) => {
    if (disabled) return;
    const publish = onProcessing?.();
    updateLocal(state => ({ ...state, pending: state.pending + 1 }));
    try {
      const imageFiles = all.filter((f) => f.type.startsWith('image/'));
      const newImages: AttachedImage[] = [];
      for (const file of imageFiles) {
        try {
          const url = await readFileAsDataURL(file);
          newImages.push({ url, mime: file.type });
        } catch (err) {
          remoteLog.error('Failed to read image', err);
        }
      }
      if (!controlled && newImages.length > 0) updateLocal(state => ({ ...state, images: [...state.images, ...newImages] }));

      const otherFiles = all.filter((f) => !f.type.startsWith('image/'));
      if (otherFiles.length === 0) { publish?.({ images: newImages, files: [] }); return; }
      const sid = sessionIdRef.current;
      if (!sid) {
        const deferred = otherFiles.map((file) => ({ path: '', name: file.name, mime: file.type || 'application/octet-stream', file }));
        publish?.({ images: newImages, files: deferred });
        if (!controlled) updateLocal(state => ({ ...state, files: [...state.files, ...deferred] }));
        return;
      }
      const newFiles: AttachedFileRef[] = [];
      for (const file of otherFiles) {
        try {
          const saved = await api.uploadComposerAttachment(sid, file, platform);
          newFiles.push({
            path: saved.path,
            name: saved.name || file.name,
            mime: saved.mime || file.type || 'application/octet-stream',
          });
        } catch (err) {
          remoteLog.error('Failed to save attachment', err);
        }
      }
      if (!controlled && newFiles.length > 0) updateLocal(state => ({ ...state, files: [...state.files, ...newFiles] }));
      publish?.({ images: newImages, files: newFiles });
    } finally {
      updateLocal(state => ({ ...state, pending: state.pending - 1 }));
    }
  }, [sessionIdRef, disabled, platform, onProcessing, controlled, updateLocal]);

  const removeImage = useCallback((index: number) => {
    if (controlled) onChange!({ images: images.filter((_, i) => i !== index), files });
    else updateLocal(state => ({ ...state, images: state.images.filter((_, i) => i !== index) }));
  }, [controlled, images, files, onChange, updateLocal]);
  const removeFile = useCallback((index: number) => {
    if (controlled) onChange!({ images, files: files.filter((_, i) => i !== index) });
    else updateLocal(state => ({ ...state, files: state.files.filter((_, i) => i !== index) }));
  }, [controlled, images, files, onChange, updateLocal]);
  const clear = useCallback(() => {
    if (controlled) onChange!({ images: [], files: [] });
    else updateLocal(state => ({ ...state, images: [], files: [] }));
  }, [controlled, onChange, updateLocal]);

  const handlePaste = useCallback((e: React.ClipboardEvent<HTMLTextAreaElement>) => {
    if (disabled) return;
    const imageFiles = Array.from(e.clipboardData.items)
      .filter((item) => item.type.startsWith('image/'))
      .map((item) => item.getAsFile())
      .filter((file): file is File => !!file);
    if (imageFiles.length === 0) return;
    e.preventDefault();
    void addFiles(imageFiles);
  }, [disabled, addFiles]);

  const handleDragOver = useCallback((e: React.DragEvent) => {
    e.preventDefault();
    e.stopPropagation();
  }, []);

  const handleDrop = useCallback((e: React.DragEvent) => {
    e.preventDefault();
    e.stopPropagation();
    if (disabled) return;
    void addFiles(Array.from(e.dataTransfer.files));
  }, [disabled, addFiles]);

  /** Prompt suffix listing on-disk attachments, or ''. */
  const uploaded = files.filter((file) => !file.file);
  const fileReferenceText = uploaded.length > 0
    ? `Attached files saved on disk:\n${uploaded.map((f) => `- ${f.path} (${f.mime})`).join('\n')}`
    : '';

  const deferredFiles = files.flatMap((file) => file.file ? [file.file] : []);
  return { images, files, deferredFiles, pending, addFiles, removeImage, removeFile, clear, handlePaste, handleDragOver, handleDrop, fileReferenceText };
}
