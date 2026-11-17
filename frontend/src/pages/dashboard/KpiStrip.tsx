import type { ReactNode } from 'react';
import { IconButton } from '../../components/IconButton';
import { Tooltip } from '../../components/Tooltip';
import './KpiStrip.css';

export type Kpi = {
  label: string;
  value: string;
  /** Secondary line under the value, in dim text. */
  detail?: ReactNode;
  /** What the figure measures; shown from an info button on hover/focus. */
  help?: string;
};

/**
 * One bordered row of headline figures. Values use text tokens rather than
 * series colours, so colour stays reserved for chart identity.
 */
export function KpiStrip({ label, items }: { label: string; items: Kpi[] }) {
  return (
    <dl className="kpi-strip" aria-label={label}>
      {items.map((item) => (
        <div key={item.label} className="kpi">
          <dt className="kpi-label">
            {item.label}
            {item.help && (
              <Tooltip content={<span className="kpi-help">{item.help}</span>}>
                <IconButton variant="ghost" className="kpi-help-button" icon="bi-info-circle" label={`About ${item.label}`} title="" />
              </Tooltip>
            )}
          </dt>
          <dd className="kpi-value">{item.value}</dd>
          {item.detail && <dd className="kpi-detail">{item.detail}</dd>}
        </div>
      ))}
    </dl>
  );
}
