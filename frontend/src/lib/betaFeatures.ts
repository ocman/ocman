import { create } from 'zustand';
import { fetchJSON, postJSON } from './api.requests';

export type BetaFeature = 'factory';

const path = '/api/settings/beta-features';
let revision = 0;

export const useBetaFeatures = create<{
  factory: boolean;
  loaded: boolean;
  error: boolean;
  load: () => Promise<void>;
  setFactory: (enabled: boolean) => Promise<void>;
}>((set) => ({
  factory: false,
  loaded: false,
  error: false,
  load: async () => {
    const current = ++revision;
    try {
      const features = await fetchJSON<{ factory: boolean }>(path);
      if (current === revision) set({ factory: features.factory === true, loaded: true, error: false });
    } catch {
      if (current === revision) set({ factory: false, error: true });
    }
  },
  setFactory: async (factory) => {
    ++revision;
    const features = await postJSON<{ factory: boolean }>(path, { factory });
    ++revision;
    set({ factory: features.factory === true, loaded: true, error: false });
  },
}));
