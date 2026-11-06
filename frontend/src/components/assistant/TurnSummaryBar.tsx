import { Fragment, useEffect, useState, type ReactNode } from 'react';
import { formatCompactNumber, formatCurrency, formatFullDateTime, formatSeconds, formatTokensPerSecond } from '../../lib/format';
import { useAgentColor } from '../../lib/agentColor';
import { useModelLabel, useTurnStats } from '../../lib/turnStats';
import { ModelLabel } from '../ModelLogo';
import { Tooltip } from '../Tooltip';
import { useMessage } from './useMessage';

/**
 * Always-visible summary bar rendered after the last assistant message of
 * each turn. Shows wall-clock duration, total tokens in+out, tool-call
 * count, and average tok/s. While the turn is still in progress the bar
 * shows whatever data is available so far.
 */
export function TurnSummaryBar({ actions }: { actions: ReactNode }) {
  const stats = useTurnStats();
  const custom = useMessage((m) => m.metadata?.custom as Record<string, unknown> | undefined);
  const agent = typeof custom?.agent === 'string' ? (custom.agent as string) : undefined;
  const agentColor = useAgentColor(agent);
  const modelLabel = useModelLabel(stats?.model || '');
  const [now, setNow] = useState(Date.now);

  // Tick every second so the live wall-clock increments visibly.
  useEffect(() => {
    if (!stats?.isLive) return;
    const id = setInterval(() => setNow(Date.now()), 1000);
    return () => clearInterval(id);
  }, [stats?.isLive]);

  // Only the turn's anchor message renders the bar. Non-anchor messages
  // still carry the aggregate (so the line never blanks out while
  // ownership moves between messages mid-turn) but must not draw it.
  if (!stats || !stats.isSummaryAnchor) return null;

  const { wallClockMs, tokensOut, tokensIn, cost, toolCalls, tps, isLive, promptCacheRebuilt, startedAt, model } = stats;

  // For a live turn, compute elapsed wall-clock from startedAt → now.
  const displayMs = isLive ? now - startedAt : wallClockMs;
  const wallSec = displayMs !== null && displayMs > 0 ? displayMs / 1000 : null;

  const totalTokens = tokensIn + tokensOut;

  // Stamp a finished turn with when it ended, not when it was sent.
  const endedAt = startedAt + (wallClockMs ?? 0);
  const time = new Date(endedAt).toLocaleTimeString('en-US', {
    hour: '2-digit', minute: '2-digit', hour12: false,
  });

  const items: ReactNode[] = [];
  if (!isLive) {
    items.push(
      <Tooltip key="time" content={formatFullDateTime(endedAt)}>
        <span className="oc-turn-stat">{time}</span>
      </Tooltip>
    );
  }
  if (wallSec !== null) {
    items.push(
      <span key="wall" className="oc-turn-stat">
        <i className="bi bi-clock" aria-hidden="true" />
        {formatSeconds(wallSec)}
      </span>
    );
  }
  if (totalTokens > 0) {
    items.push(
      <span key="tok" className="oc-turn-stat">
        <i className="bi bi-stars" aria-hidden="true" />
        {formatCompactNumber(totalTokens)} tok
      </span>
    );
  }
  if (toolCalls > 0) {
    items.push(
      <span key="tools" className="oc-turn-stat">
        <i className="bi bi-tools" aria-hidden="true" />
        {toolCalls} {toolCalls === 1 ? 'tool' : 'tools'}
      </span>
    );
  }
  if (tps !== null) {
    items.push(
      <span key="tps" className="oc-turn-stat" title="Estimated output tokens per model-request second, excluding recorded tool and approval waits; includes startup latency.">
        {formatTokensPerSecond(tps)} tok/s
      </span>
    );
  }
  if (cost > 0) {
    items.push(
      <span key="cost" className="oc-turn-stat">
        {formatCurrency(cost, cost < 0.001 ? 4 : 2)}
      </span>
    );
  }
  if (promptCacheRebuilt) {
    items.push(
      <span key="cache" className="oc-turn-stat oc-turn-cache-warning">
        <i className="bi bi-arrow-clockwise" aria-hidden="true" />
        Prompt cache rebuilt
      </span>
    );
  }
  if (model) {
    items.push(
      <span key="model" className="oc-turn-stat oc-turn-model" title={model}>
        <ModelLabel model={model} muted>{modelLabel}</ModelLabel>
      </span>
    );
  }

  if (items.length === 0 && !isLive) return null;

  return (
    <div className="oc-turn-stats">
      <span
        className={`oc-turn-dot${isLive ? ' oc-turn-dot--live' : ''}`}
        style={agent ? { background: agentColor } : undefined}
        title={isLive ? 'Turn in progress' : undefined}
      />
      {items.map((item, i) => (
        <Fragment key={i}>
          {item}
          {i < items.length - 1 && <span className="oc-turn-sep">·</span>}
        </Fragment>
      ))}
      {!isLive && <span className="oc-turn-actions" role="group" aria-label="Turn actions">{actions}</span>}
    </div>
  );
}
