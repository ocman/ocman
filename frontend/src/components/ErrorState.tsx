import type { ReactNode } from 'react';
import { PageState } from './PageState';

/** An unavailable state; `inline` fits it into a pane. Kept for the plugin UI SDK. */
export function ErrorState({ title, description, inline = false, children }: { title: string; description?: ReactNode; inline?: boolean; children?: ReactNode }) {
  return <PageState error compact={inline} title={title} description={description}>{children}</PageState>;
}
