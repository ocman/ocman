import type { ReactNode } from 'react';
import { FactoryActionCard } from './FactoryActionCard';
import { FactoryEpicCard } from './FactoryEpicCard';

// Renders a parsed [[ocman:card ...]] marker, from assistant text or a tool result.
export function FactoryMarkerCard({ epicID, issueID, action, children }: { epicID: string; issueID: string; action: string; children?: ReactNode }) {
  return action === 'created'
    ? <FactoryEpicCard epicID={epicID}>{children}</FactoryEpicCard>
    : <FactoryActionCard key={`${epicID}/${issueID}/${action}`} epicID={epicID} issueID={issueID} requestedAction={action}>{children}</FactoryActionCard>;
}
