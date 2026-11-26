import type { ReactNode } from 'react';
import { useModelLabel } from '../../lib/turnStats';
import { ModelLabel } from '../ModelLogo';

function ConversationDivider({ children, label, title, testId }: {
  children: ReactNode;
  label?: string;
  title?: string;
  testId?: string;
}) {
  return (
    <div className="oc-model-change" role="separator" aria-label={label} data-testid={testId}>
      <span className="oc-model-change-line" aria-hidden="true" />
      <span className="oc-model-change-chip" title={title}>{children}</span>
      <span className="oc-model-change-line" aria-hidden="true" />
    </div>
  );
}

export function ModelChangeDivider({ model }: { model: string }) {
  const label = useModelLabel(model);
  return <ConversationDivider title={model} testId="model-change-divider">
    Model changed to <ModelLabel model={model}>{label}</ModelLabel>
  </ConversationDivider>;
}

export function ContextCompactionDivider() {
  const label = 'Context compaction';
  return <ConversationDivider label={label}>{label}</ConversationDivider>;
}
