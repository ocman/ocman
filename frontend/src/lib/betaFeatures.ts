import { create } from 'zustand';
import { persist } from 'zustand/middleware';

export type BetaFeature = 'factory';

export const useBetaFeatures = create<{ factory: boolean; setFactory: (enabled: boolean) => void }>()(
  persist((set) => ({
    factory: false,
    setFactory: (factory) => set({ factory }),
  }), {
    name: 'ocman-beta-features',
    partialize: ({ factory }) => ({ factory }),
  }),
);
