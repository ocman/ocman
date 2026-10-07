import type { AttachedImage, AttachedFileRef } from '../components/assistant/useComposerAttachments';
import { getDraftVersion } from './composerDraft';
import { create } from 'zustand';

type Payload = { images: AttachedImage[]; files: AttachedFileRef[]; pending?: number };
const usePayloads = create<{ payloads: Map<string, Payload> }>(() => ({ payloads: new Map() }));
const empty: Payload = { images: [], files: [] };
export const usePendingDraftPayload = (id: string) => usePayloads((state) => state.payloads.get(id) || empty);

export function rememberPendingDraftPayload(draftId: string, images: AttachedImage[] = [], files: File[] = []) {
  updateDraftAttachments(draftId, { images,
    files: files.map((file) => ({ file, path: '', name: file.name, mime: file.type || 'application/octet-stream' })) });
}

export function getPendingDraftPayload(draftId: string) {
  return usePayloads.getState().payloads.get(draftId);
}

export const updateDraftAttachments = (draftId: string, payload: Payload) => usePayloads.setState((state) => ({ payloads: new Map(state.payloads).set(draftId, { ...state.payloads.get(draftId), ...payload }) }));
export const forgetDraftAttachments = (draftId: string) => usePayloads.setState((state) => {
  const payloads = new Map(state.payloads);
  payloads.delete(draftId);
  return { payloads };
});

export function transferDraftAttachments(from: string, to: string) {
  const payload = getPendingDraftPayload(from);
  if (payload) updateDraftAttachments(to, payload);
}

/** Capture the initiating identity before FileReader yields, merge accepted batches later. */
export function pendingAttachmentWriter(draftId: string) {
  const version = getDraftVersion(draftId);
  const accepted = getPendingDraftPayload(draftId) || empty;
  updateDraftAttachments(draftId, { ...accepted, pending: (accepted.pending || 0) + 1 });
  return (batch: { images: AttachedImage[]; files: AttachedFileRef[] }) => {
    if (getDraftVersion(draftId) !== version) return;
    const current = getPendingDraftPayload(draftId) || empty;
    updateDraftAttachments(draftId, { images: [...current.images, ...batch.images], files: [...current.files, ...batch.files], pending: Math.max(0, (current.pending || 0) - 1) });
  };
}
