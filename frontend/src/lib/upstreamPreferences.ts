import { create } from 'zustand';
import { persist } from 'zustand/middleware';
import type { StateFilter } from './upstreamApi';

interface UpstreamPreferences {
  tab: 'prs' | 'issues';
  prState: StateFilter;
  issueState: StateFilter;
  prMine: boolean;
  issueMine: boolean;
}

interface UpstreamPreferencesStore {
  preferences: UpstreamPreferences;
  setPreferences: (patch: Partial<UpstreamPreferences>) => void;
}

export const useUpstreamPreferences = create<UpstreamPreferencesStore>()(
  persist(
    (set) => ({
      preferences: { tab: 'prs', prState: 'open', issueState: 'open', prMine: false, issueMine: false },
      setPreferences: (patch) => set((state) => ({ preferences: { ...state.preferences, ...patch } })),
    }),
    {
      name: 'ocman-upstream-preferences',
      partialize: (state) => ({ preferences: state.preferences }),
    },
  ),
);
