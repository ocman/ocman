import { useCallback, useEffect, useState } from 'react';
import type { ReactNode } from 'react';
import { DiffFullscreenModal, type FullscreenDiffFile } from './DiffFullscreenModal';

// useFullscreenDiff owns the open/closed state for a sidebar's
// fullscreen diff browser and registers the open callback with an
// embedded parent (RightPanel) via onFullscreen, mirroring the
// onRefresh contract. Returns the open callback for the standalone
// header's button plus the modal element (null while closed).
//
// Lives in its own file (not DiffFullscreenModal.tsx) so the modal
// file only exports components, which React Fast Refresh requires.
export function useFullscreenDiff(
  title: string,
  files: FullscreenDiffFile[],
  onFullscreen?: (open: () => void) => void,
): { open: () => void; modal: ReactNode } {
  const [fullscreen, setFullscreen] = useState(false);
  const open = useCallback(() => setFullscreen(true), []);
  useEffect(() => {
    onFullscreen?.(open);
  }, [onFullscreen, open]);
  const modal = fullscreen ? (
    <DiffFullscreenModal title={title} files={files} onClose={() => setFullscreen(false)} />
  ) : null;
  return { open, modal };
}
