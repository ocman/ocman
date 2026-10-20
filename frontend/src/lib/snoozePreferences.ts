import { create } from 'zustand';
import { persist } from 'zustand/middleware';
import { DEFAULT_SNOOZE_CHOICES, type SnoozeChoice } from './snooze';

interface SnoozePreferences {
  choices: SnoozeChoice[];
  setChoices: (choices: SnoozeChoice[]) => void;
}

export const useSnoozePreferences = create<SnoozePreferences>()(persist(
  (set) => ({ choices: DEFAULT_SNOOZE_CHOICES, setChoices: (choices) => set({ choices }) }),
  { name: 'ocman-snooze-choices', partialize: (state) => ({ choices: state.choices }) },
));
