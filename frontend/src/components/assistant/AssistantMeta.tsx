import { formatFullDateTime, formatSeconds, formatTokensPerSecond } from '../../lib/format';
import { isMutedTool } from '../../lib/mutedTools';
import { useModelLabel } from '../../lib/turnStats';
import { useUiStore } from '../../lib/uiStore';
import { ModelLabel } from '../ModelLogo';
import { Tooltip } from '../Tooltip';
import { useMessage } from './useMessage';

export function AssistantMeta() {
  const showMessageMetadata = useUiStore((s) => s.showMessageMetadata);
  const createdAt = useMessage((m) => m.createdAt);
  const status = useMessage((m) => m.status);
  const content = useMessage((m) => m.content);
  const custom = useMessage((m) => m.metadata?.custom as Record<string, unknown> | undefined);
  const agent = typeof custom?.agent === 'string' ? (custom.agent as string) : undefined;
  const model = typeof custom?.model === 'string' ? (custom.model as string) : '';
  const modelLabel = useModelLabel(model);
  if (!createdAt || createdAt.getTime() === 0) return null;
  if (status?.type === 'running') return null;
  // Hide timestamp when message only contains file reads
  const onlyReads = content.every(
    (p) => {
      if (p.type === 'text' && 'text' in p && !(p as { text: string }).text.trim()) return true;
      if (p.type !== 'tool-call' || !('toolName' in p)) return false;
      return isMutedTool((p as { toolName: string }).toolName);
    }
  );
  if (onlyReads) return null;
  const time = createdAt.toLocaleTimeString('en-US', { hour: '2-digit', minute: '2-digit', hour12: false });
  const isError = status?.type === 'incomplete' && 'reason' in status && status.reason === 'error';
  const errorName = custom?.errorName as string | undefined;
  const isAbort = errorName === 'MessageAbortedError' || errorName === 'AbortError';

  // Duration is elapsed time; TPS comes from the tool-adjusted sample.
  const msgTime = custom?.time as { created?: number; completed?: number } | undefined;
  let durationSec: number | null = null;
  const tps = typeof custom?.tokensPerSecond === 'number' ? custom.tokensPerSecond : null;
  if (msgTime?.created && msgTime?.completed) {
    const d = (msgTime.completed - msgTime.created) / 1000;
    if (d > 0) {
      durationSec = d;
    }
  }

  return (
    <>
      {isError && (
        <div className={`oc-thread-error-note${isAbort ? ' oc-thread-error-note-abort' : ''}`}>
          {isAbort
            ? <><i className="bi bi-slash-circle" aria-hidden="true" /> Interrupted</>
            : <><i className="bi bi-exclamation-triangle-fill" aria-hidden="true" /> Session ended with an error</>
          }
        </div>
      )}
      {showMessageMetadata && <div className="oc-msg-meta">
        <span
          className="oc-meta-dot"
          style={isError ? { background: 'var(--danger)' } : undefined}
          title={isError ? 'Error' : agent ? `agent: ${agent}` : 'Message group'}
        />
        <Tooltip content={formatFullDateTime(createdAt.getTime())}>
          <time dateTime={createdAt.toISOString()}>{time}</time>
        </Tooltip>
        {durationSec !== null && (
          <>
            <span className="oc-meta-sep">·</span>
            <span className="oc-meta-tps">{formatSeconds(durationSec)}</span>
          </>
        )}
        {tps !== null && (
          <>
            <span className="oc-meta-sep">·</span>
            <span className="oc-meta-tps" title="Estimated output tokens per model-request second, excluding recorded tool and approval waits; includes startup latency.">{formatTokensPerSecond(tps)} tok/s</span>
          </>
        )}
        {model && (
          <>
            <span className="oc-meta-sep">·</span>
            <span className="oc-meta-model" title={model}>
              <ModelLabel model={model} muted>{modelLabel}</ModelLabel>
            </span>
          </>
        )}
      </div>}
    </>
  );
}
