import * as RadixTooltip from '@radix-ui/react-tooltip';
import type { FC, ReactElement, ReactNode } from 'react';
import './Tooltip.css';

/** One hover delay for every tooltip in the app. */
export const TOOLTIP_DELAY_MS = 300;

/**
 * Hover/focus tooltip. `children` must be a single element that accepts a
 * ref (it becomes the trigger via `asChild`).
 */
export const Tooltip: FC<{ content: ReactNode; children: ReactElement }> = ({ content, children }) => (
  <RadixTooltip.Provider delayDuration={TOOLTIP_DELAY_MS}>
    <RadixTooltip.Root>
      <RadixTooltip.Trigger asChild>{children}</RadixTooltip.Trigger>
      <RadixTooltip.Portal>
        <RadixTooltip.Content className="oc-tooltip" sideOffset={4}>
          {content}
        </RadixTooltip.Content>
      </RadixTooltip.Portal>
    </RadixTooltip.Root>
  </RadixTooltip.Provider>
);
