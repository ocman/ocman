import { create } from 'zustand';

export interface StartupStatus {
  state: 'starting' | 'ready' | 'failed';
  phase: string;
  completed: number;
  total: number;
  elapsedSeconds: number;
}

// Diagnostics never decide whether the authenticated app stays mounted.
export const useStartupStatus = create<{ status: StartupStatus | null; unreachable: boolean }>(() => ({
  status: null,
  unreachable: false,
}));
