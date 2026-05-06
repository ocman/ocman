import { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import type { Dispatch, MutableRefObject, SetStateAction } from 'react';
import type { Part } from '../../lib/api';
import { api } from '../../lib/api';
import { extractTaskId, isTaskTool } from '../../lib/taskId';

/**
 * Maximum number of per-message token entries we keep in the
 * subagent token map. The map is the input to the live-tokens-per-
 * second indicator; without a bound a long subagent stream would
 * grow it unboundedly.
 */
const MAX_SUBAGENT_TOKEN_ENTRIES = 256;

/** Drop the oldest entries when the map outgrows the cap. */
function trimSubagentTokens(
  prev: Map<string, { output: number; created: number }>,
): Map<string, { output: number; created: number }> {
  if (prev.size <= MAX_SUBAGENT_TOKEN_ENTRIES) return prev;
  const entries = Array.from(prev.entries());
  return new Map(entries.slice(entries.length - MAX_SUBAGENT_TOKEN_ENTRIES));
}

/** One row of an in-flight task's status. */
export interface RunningTaskEntry {
  taskId: string;
  status: string;
}

/** Per-message token snapshot used by the TPS indicator. */
export type SubagentTokenMap = Map<string, { output: number; created: number }>;

/**
 * Public surface of useSubagentTracking. The setters are exposed so
 * the SSE handler can drop new token observations into the same map
 * without re-importing the hook's internals.
 */
export interface UseSubagentTrackingResult {
  /** Set of subagent session ids derived from task-tool parts. */
  subagentSessionIds: Set<string>;
  /**
   * Ref-mirror of `subagentSessionIds`. The SSE effect reads this so
   * it can route subagent events without re-subscribing every time
   * the set changes.
   */
  subagentSessionIdsRef: MutableRefObject<Set<string>>;
  /** In-flight task entries, derived from task-tool parts. */
  runningTaskIds: RunningTaskEntry[];
  /** Per-message token snapshots, keyed by subagent message id. */
  subagentTokens: SubagentTokenMap;
  setSubagentTokens: Dispatch<SetStateAction<SubagentTokenMap>>;
  /** Live stdout per subagent task, keyed by task id. */
  taskLiveOutput: Record<string, string>;
  setTaskLiveOutput: Dispatch<SetStateAction<Record<string, string>>>;
}

/**
 * Track subagent (`task` / `mcp_task`) progress for a session.
 *
 * Owns three pieces of state:
 *   1. The set of subagent session ids referenced by the page's
 *      parts. The SSE handler uses this to recognise events from
 *      subagent sessions (their session id differs from the page's).
 *   2. The token-output snapshot per subagent assistant message,
 *      used to compute live tokens-per-second across subagent runs.
 *   3. The live stdout cache per running task, fed to the runtime
 *      provider so the assistant thread can show streaming output
 *      while a subagent is still going.
 *
 * The hook also runs the 2-second poll against /api/session/{id}/tasks
 * so live stdout stays fresh while at least one subagent is running.
 */
export function useSubagentTracking(
  parts: Part[],
  sessionId: string | undefined,
): UseSubagentTrackingResult {
  const [subagentTokens, setSubagentTokensRaw] = useState<SubagentTokenMap>(new Map());
  const [taskLiveOutput, setTaskLiveOutput] = useState<Record<string, string>>({});

  // Wrap the setter in a stable reference that always trims trailing
  // entries past the cap. Callers can safely pass plain SetStateAction
  // values without worrying about the cap.
  //
  // useCallback with `[]` keeps the function identity stable across
  // renders: downstream `useEffect`s that depend on this setter (e.g.
  // useSessionStatus's 1 Hz polling effect) won't tear down + re-arm
  // on every parent rerender, which would otherwise pin the TPS
  // refresh rate to the SSE delta rate.
  const setSubagentTokens = useCallback<Dispatch<SetStateAction<SubagentTokenMap>>>((next) => {
    setSubagentTokensRaw((prev) => {
      const updated = typeof next === 'function' ? (next as (p: SubagentTokenMap) => SubagentTokenMap)(prev) : next;
      return trimSubagentTokens(updated);
    });
  }, []);

  // Derive subagent session IDs from task-tool parts. Recomputes
  // whenever parts changes; the result is fed into a ref so the SSE
  // effect doesn't need it as a dependency.
  const subagentSessionIds = useMemo(() => {
    const ids = new Set<string>();
    for (const p of parts) {
      const d = typeof p.data === 'string'
        ? (() => { try { return JSON.parse(p.data); } catch { return null; } })()
        : p.data;
      if (!d || typeof d !== 'object') continue;
      const toolName = (d as Record<string, unknown>).tool as string | undefined;
      if (!isTaskTool(toolName)) continue;
      const st = (d as Record<string, unknown>).state as Record<string, unknown> | undefined;
      const taskId = extractTaskId(st);
      if (taskId) ids.add(taskId);
    }
    return ids;
  }, [parts]);

  // Ref-mirror so the SSE effect reads the latest set without
  // re-running on every change.
  const subagentSessionIdsRef = useRef<Set<string>>(subagentSessionIds);
  subagentSessionIdsRef.current = subagentSessionIds;

  // Derive in-flight task ids — ones whose state.status === 'running'.
  const runningTaskIds = useMemo(() => {
    const running: RunningTaskEntry[] = [];
    for (const p of parts) {
      const d = typeof p.data === 'string'
        ? (() => { try { return JSON.parse(p.data); } catch { return null; } })()
        : p.data;
      if (!d || typeof d !== 'object') continue;
      const toolName = (d as Record<string, unknown>).tool as string | undefined;
      if (!isTaskTool(toolName)) continue;
      const st = (d as Record<string, unknown>).state as Record<string, unknown> | undefined;
      const status = (st?.status as string) || 'running';
      if (status !== 'running') continue;
      const taskId = extractTaskId(st);
      if (taskId) running.push({ taskId, status });
    }
    return running;
  }, [parts]);

  // Poll the running tasks for their live stdout. Effect re-fires
  // whenever the *count* of running tasks changes — using the contents
  // would re-create the interval on every status flip, which costs an
  // extra request without any payoff.
  useEffect(() => {
    if (!sessionId || runningTaskIds.length === 0) return;
    const controller = new AbortController();
    const taskIdList = runningTaskIds.map(({ taskId }) => taskId);
    const poll = async () => {
      try {
        const resp = await api.sessionTasks(sessionId, taskIdList, controller.signal);
        if (controller.signal.aborted) return;
        const tasks = resp.tasks || {};
        if (Object.keys(tasks).length > 0) {
          setTaskLiveOutput((prev) => ({ ...prev, ...tasks }));
        }
      } catch {
        /* ignore poll errors — next tick retries */
      }
    };
    poll();
    const interval = setInterval(poll, 2000);
    return () => {
      controller.abort();
      clearInterval(interval);
    };
    // Only the count matters: contents flipping from running to done
    // is itself signalled by the next memo recomputation.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [runningTaskIds.length, sessionId]);

  return {
    subagentSessionIds,
    subagentSessionIdsRef,
    runningTaskIds,
    subagentTokens,
    setSubagentTokens,
    taskLiveOutput,
    setTaskLiveOutput,
  };
}
