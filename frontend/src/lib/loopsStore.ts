import { create } from 'zustand';
import { api } from './api';
import type { Loop, LoopCreateRequest, LoopUpdateRequest } from './api.types';
import { onLoopUpdated } from './useGlobalEvents';

/**
 * Global store for agent loops. Holds the current list (optionally
 * scoped to a session/directory) and refreshes it on `loop.updated`
 * broadcasts (AD-10) so the Loops view updates live without polling.
 */
type LoopsStore = {
  loops: Loop[];
  loading: boolean;
  error: string | null;
  /** Current filter; used to refetch on a loop.updated broadcast. */
  filter: { session?: string; dir?: string };

  load: (filter?: { session?: string; dir?: string }) => Promise<void>;
  /** Re-run the last load with its filter. */
  refresh: () => Promise<void>;
  remove: (id: string) => Promise<void>;
  pause: (id: string) => Promise<void>;
  resume: (id: string) => Promise<void>;
  step: (id: string) => Promise<void>;
  /** Force a schedule loop to fire now (bypasses interval). */
  trigger: (id: string) => Promise<void>;
  /** Edit a loop's safe-to-change settings. */
  update: (id: string, req: LoopUpdateRequest) => Promise<void>;
  /** Create a new loop, then refresh the list. */
  create: (req: LoopCreateRequest) => Promise<void>;
};

export const useLoopsStore = create<LoopsStore>((set, get) => ({
  loops: [],
  loading: false,
  error: null,
  filter: {},

  load: async (filter) => {
    const f = filter ?? get().filter;
    set({ loading: true, error: null, filter: f });
    try {
      const loops = await api.loops.list(f);
      set({ loops, loading: false });
    } catch (e) {
      set({ loading: false, error: e instanceof Error ? e.message : String(e) });
    }
  },

  refresh: async () => {
    try {
      const loops = await api.loops.list(get().filter);
      set({ loops });
    } catch {
      // Keep the existing list on a transient refresh failure.
    }
  },

  remove: async (id) => {
    await api.loops.delete(id);
    await get().refresh();
  },
  pause: async (id) => {
    await api.loops.pause(id);
    await get().refresh();
  },
  resume: async (id) => {
    await api.loops.resume(id);
    await get().refresh();
  },
  step: async (id) => {
    await api.loops.step(id);
    await get().refresh();
  },
  trigger: async (id) => {
    await api.loops.trigger(id);
    await get().refresh();
  },
  update: async (id, req) => {
    await api.loops.update(id, req);
    await get().refresh();
  },
  create: async (req) => {
    await api.loops.create(req);
    await get().refresh();
  },
}));

// Refresh the store whenever any loop changes server-side.
onLoopUpdated(() => {
  void useLoopsStore.getState().refresh();
});
