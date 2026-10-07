import type { StartSessionResponse } from './api.types';

export interface DraftStart {
  version: number;
  text: string;
  sessionId?: string;
  error?: string;
  routeKey?: string;
  createdSession?: Pick<StartSessionResponse, 'sessionId' | 'platform' | 'remoteId' | 'directory'>;
  attemptId?: string;
  replacementDraftId?: string;
  persistenceError?: string;
  committed?: boolean;
  relocationError?: string;
  pendingReplacementId?: string;
  retirement?: string;
  deliveryState?: 'pending' | 'failed' | 'interrupted' | 'done';
  deliveryOwner?: string;
}

// IndexedDB readwrite transactions serialize claims across tabs, including plain HTTP.
function transact(draftId: string, change?: (current: DraftStart | undefined) => DraftStart): Promise<DraftStart | undefined> {
  return new Promise((resolve, reject) => {
    if (typeof indexedDB === 'undefined') {
      reject(new Error('This browser cannot coordinate session starts.'));
      return;
    }
    const open = indexedDB.open('ocman.preparedDraftStarts.v1', 1);
    let blocked = false;
    open.onupgradeneeded = () => open.result.createObjectStore('starts');
    open.onerror = () => reject(open.error || new Error('Could not open the draft start database'));
    open.onblocked = () => {
      blocked = true;
      reject(new Error('Close other ocman tabs to update the draft start database'));
    };
    open.onsuccess = () => {
      const db = open.result;
      if (blocked) { db.close(); return; }
      let transaction: IDBTransaction;
      try { transaction = db.transaction('starts', change ? 'readwrite' : 'readonly'); }
      catch (error) { db.close(); reject(error); return; }
      const store = transaction.objectStore('starts');
      const read = store.get(draftId);
      let result: DraftStart | undefined;
      read.onsuccess = () => {
        const current = read.result as DraftStart | undefined;
        result = change ? change(current) : current;
        if (change && result !== current) store.put(result, draftId);
      };
      transaction.oncomplete = () => { db.close(); resolve(result); };
      transaction.onabort = () => { db.close(); reject(transaction.error || new Error('Could not save the draft start claim')); };
    };
  });
}

export async function claimDraftStart(draftId: string, next: DraftStart) {
  let claimed = false;
  const start = await transact(draftId, (current) => {
    if (current && !current.error) return current;
    claimed = true;
    return next;
  });
  return { claimed, start: start! };
}

export const persistDraftStart = (draftId: string, start: DraftStart) => transact(draftId, (current) =>
  current && (current.attemptId !== start.attemptId || current.deliveryState === 'done') ? current : start);
export const readDraftStart = (draftId: string) => transact(draftId);
