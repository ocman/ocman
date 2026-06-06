import {
  SIDEBAR_DEFAULT_WIDTH,
  SIDEBAR_MAX_WIDTH,
  SIDEBAR_MIN_WIDTH,
  useUiStore,
} from '../lib/uiStore';
import { Resizer } from './Resizer';

/**
 * Vertical drag handle on the right edge of `.session-sidebar`. Thin
 * wrapper over the shared Resizer bound to the left-sidebar ui-store
 * width.
 */
export function SidebarResizer() {
  const width = useUiStore((s) => s.sidebarWidth);
  const setWidth = useUiStore((s) => s.setSidebarWidth);
  return (
    <Resizer
      width={width}
      setWidth={setWidth}
      min={SIDEBAR_MIN_WIDTH}
      max={SIDEBAR_MAX_WIDTH}
      defaultWidth={SIDEBAR_DEFAULT_WIDTH}
      ariaLabel="Resize sidebar"
    />
  );
}
