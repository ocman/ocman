// Shared @pierre/diffs render options for all diff viewers in ocman.
// Uses the catppuccin-mocha Shiki theme (bundled in shiki) so syntax
// highlighting matches the rest of the app's Catppuccin Mocha palette.
// A small unsafeCSS block fine-tunes the diff-specific colours that the
// theme doesn't control (row tints, line numbers, font).

import type { MultiFileDiffProps } from '@pierre/diffs/react';

const CATPPUCCIN_CSS = `
  :host {
    /* Row background tints — subtle green/red wash on changed lines */
    --diffs-bg-addition-override: rgba(166, 227, 161, 0.1);
    --diffs-bg-deletion-override: rgba(243, 139, 168, 0.1);

    /* Line number colour: Catppuccin overlay0 */
    --diffs-fg-number-override: #6c7086;

    /* Font matches app mono stack */
    --diffs-font-family: 'JetBrains Mono', 'SF Mono', Consolas, monospace;
    --diffs-font-size: 12px;
    --diffs-line-height: 18px;
  }
`;

export const DIFF_OPTIONS: NonNullable<MultiFileDiffProps<undefined>['options']> = {
  theme: 'catppuccin-mocha',
  diffStyle: 'unified',
  disableFileHeader: true,
  overflow: 'wrap',
  diffIndicators: 'none',
  unsafeCSS: CATPPUCCIN_CSS,
};
