import { timeUntilISO } from '../../lib/format';
import type { ModelEntry } from './modelEntries';
import './ModelPicker.css';

export function ModelBadges({ entry: e, rich }: { entry: ModelEntry; rich: boolean }) {
  const cooldownLeft = e.cooldownUntil ? timeUntilISO(e.cooldownUntil) : '';
  return (
    <>
      {e.isSessionDefault && (
        <span className="oc-model-picker-badge oc-model-picker-badge--star" title="Session default">
          <i className="bi bi-star-fill" />
        </span>
      )}
      {!e.isSessionDefault && e.recentRank > 0 && (
        <span className="oc-model-picker-badge oc-model-picker-badge--used" title="Recently used">
          <i className="bi bi-clock-history" />
        </span>
      )}
      {e.isProviderDefault && !e.isSessionDefault && (
        <span className="oc-model-picker-badge oc-model-picker-badge--default" title="Provider default">
          default
        </span>
      )}
      {rich && !e.isAvailable && (
        <span className="oc-model-picker-badge oc-model-picker-badge--archived" title="Provider not connected">
          archived
        </span>
      )}
      {cooldownLeft && (
        <span
          className="oc-model-picker-badge oc-model-picker-badge--cooldown"
          title="Provider is cooling down; prompts fall through to the next project model"
        >
          <i className="bi bi-hourglass-split" /> unavailable · {cooldownLeft}
        </span>
      )}
    </>
  );
}
