import type { AttachedImage, AttachedFileRef } from '../components/assistant/useComposerAttachments';
import { create } from 'zustand';

type Payload = { images: AttachedImage[]; files: AttachedFileRef[]; pending?: number };
const usePayloads = create<{ payloads: Map<string, Payload> }>(() => ({ payloads: new Map() }));
const empty: Payload = { images: [], files: [] };
const owners = new Map<string, { draftId: string; cancelled: boolean }>();
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
  const owner = owners.get(draftId);
  if (owner?.draftId === draftId) owner.cancelled = true;
  owners.delete(draftId);
  const payloads = new Map(state.payloads);
  payloads.delete(draftId);
  return { payloads };
});

export function transferDraftAttachments(from: string, to: string) {
  const payload = getPendingDraftPayload(from);
  if (!payload || from === to) return;
  usePayloads.setState((state) => {
    const payloads = new Map(state.payloads).set(to, payload);
    payloads.delete(from);
    return { payloads };
  });
  const owner = owners.get(from);
  if (owner) { owner.draftId = to; owners.set(to, owner); }
}

/** Only attachment discard cancels accepted batches; clearing text does not. */
export function pendingAttachmentWriter(draftId: string) {
  const owner = owners.get(draftId) || { draftId, cancelled: false };
  owners.set(draftId, owner);
  const accepted = getPendingDraftPayload(draftId) || empty;
  updateDraftAttachments(draftId, { ...accepted, pending: (accepted.pending || 0) + 1 });
  return (batch: { images: AttachedImage[]; files: AttachedFileRef[] }) => {
    if (owner.cancelled) return;
    const current = getPendingDraftPayload(owner.draftId) || empty;
    updateDraftAttachments(owner.draftId, { images: [...current.images, ...batch.images], files: [...current.files, ...batch.files], pending: Math.max(0, (current.pending || 0) - 1) });
  };
}

/** Tests only. */
export function resetDraftPayloadsForTests() {
  owners.clear();
  usePayloads.setState({ payloads: new Map() });
}
