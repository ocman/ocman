import { useEffect, useState } from 'react';
import { useLocation, useNavigate, useNavigationType, type Location } from 'react-router-dom';
import { useConfirm } from './useConfirm';

function selection(location: Location) {
  const params = new URLSearchParams(location.search);
  return JSON.stringify([params.get('ownerId'), params.get('plugin'), params.get('pluginTab') || 'settings']);
}

function browserIndex(location: Location): number | undefined {
  const state = window.history.state;
  return state?.key === location.key && Number.isInteger(state.idx) ? state.idx : undefined;
}

/** Keep the editor mounted during a history-driven discard decision. */
export function usePluginSettingsLocation() {
  const location = useLocation();
  const navigationType = useNavigationType();
  const navigate = useNavigate();
  const confirm = useConfirm();
  const [dirty, setDirty] = useState(false);
  const [history, setHistory] = useState(() => {
    const index = browserIndex(location) ?? 0;
    return { observed: location, displayed: location, index: index as number | undefined, displayedIndex: index as number | undefined,
      entries: { [location.key]: index } as Record<string, number | undefined>, pending: null as { location: Location; index: number | undefined } | null };
  });

  // Adjust before rendering children: a POP must not unmount the draft first.
  if (history.observed.key !== location.key) {
    const index = navigationType === 'PUSH' ? (history.index ?? 0) + 1
      : navigationType === 'REPLACE' ? history.index : history.entries[location.key] ?? browserIndex(location);
    const blocked = navigationType === 'POP' && dirty && selection(history.displayed) !== selection(location);
    setHistory({ observed: location, index, entries: { ...history.entries, [location.key]: index },
      displayed: blocked ? history.displayed : location, displayedIndex: blocked ? history.displayedIndex : index,
      pending: blocked ? { location, index } : null });
  }

  useEffect(() => {
    const pending = history.pending;
    if (!pending) return;
    let active = true;
    void confirm({ title: 'Discard unsaved changes?', description: 'Your plugin configuration has not been saved.',
      confirmLabel: 'Discard changes', cancelLabel: 'Keep editing', destructive: true }).then((discard) => {
      if (!active) return;
      if (discard) {
        setDirty(false);
        setHistory((previous) => ({ ...previous, displayed: pending.location, displayedIndex: pending.index, pending: null }));
      } else {
        setHistory((previous) => ({ ...previous, pending: null }));
        if (history.displayedIndex !== undefined && pending.index !== undefined) {
          // Restore the original entry, preserving both Back and Forward stacks.
          void navigate(history.displayedIndex - pending.index);
        } else {
          // An entry predating this mounted editor may not have an index.
          void navigate(history.displayed, { replace: true, state: history.displayed.state });
        }
      }
    });
    return () => { active = false; };
  }, [history.pending, history.displayed, history.displayedIndex, confirm, navigate]);

  return { params: new URLSearchParams(history.displayed.search), dirty, setDirty };
}
