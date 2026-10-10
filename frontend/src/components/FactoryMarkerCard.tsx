import type { ReactNode } from 'react';
import { FactoryActionCard } from './FactoryActionCard';
import { FactoryEpicCard } from './FactoryEpicCard';
import { FactoryControlCard } from './FactoryControlCard';

// Renders a parsed [[ocman:card ...]] marker, from assistant text or a tool result.
export function FactoryMarkerCard({ epicID, issueID, action, children }: { epicID: string; issueID: string; action: string; children?: ReactNode }) {
  if (action === 'control') return <FactoryControlCard epicID={epicID} actionID={issueID} />;
  return action === 'created'
    ? <FactoryEpicCard epicID={epicID}>{children}</FactoryEpicCard>
    : <FactoryActionCard key={`${epicID}/${issueID}/${action}`} epicID={epicID} issueID={issueID} requestedAction={action}>{children}</FactoryActionCard>;
}
