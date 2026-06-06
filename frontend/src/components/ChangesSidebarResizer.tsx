import {
  CHANGES_SIDEBAR_DEFAULT_WIDTH,
  CHANGES_SIDEBAR_MAX_WIDTH,
  CHANGES_SIDEBAR_MIN_WIDTH,
  useUiStore,
} from '../lib/uiStore';
import { Resizer } from './Resizer';

/**
 * Vertical drag handle on the LEFT edge of `.oc-changes-sidebar`. Thin
 * wrapper over the shared Resizer in mirrored mode: because the right
 * sidebar is anchored to the right edge, dragging the handle left grows
 * it, so the pointer delta and arrow keys are inverted.
 */
export function ChangesSidebarResizer() {
  const width = useUiStore((s) => s.changesSidebarWidth);
  const setWidth = useUiStore((s) => s.setChangesSidebarWidth);
  return (
    <Resizer
      width={width}
      setWidth={setWidth}
      min={CHANGES_SIDEBAR_MIN_WIDTH}
      max={CHANGES_SIDEBAR_MAX_WIDTH}
      defaultWidth={CHANGES_SIDEBAR_DEFAULT_WIDTH}
      mirrored
      ariaLabel="Resize changes sidebar"
    />
  );
}
