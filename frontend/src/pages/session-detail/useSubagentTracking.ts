import { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import type { Dispatch, MutableRefObject, SetStateAction } from 'react';
import type { ChildSessionReference, Part, TaskSessionData } from '../../lib/api';
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
  /** Sub-session data per subagent task, keyed by task id. */
  taskLiveOutput: Record<string, TaskSessionData>;
  setTaskLiveOutput: Dispatch<SetStateAction<Record<string, TaskSessionData>>>;
  childSessions: ChildSessionReference[];
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
 *   3. The sub-session data cache per task, fed to the runtime
 *      provider so the assistant thread can render an embedded
 *      thread preview of the subagent conversation.
 *
 * The hook runs a 2-second poll against /api/session/{id}/tasks for
 * running tasks, and a one-shot fetch for completed tasks whose
 * sub-session data hasn't been loaded yet.
 */
export function useSubagentTracking(
  parts: Part[],
  sessionId: string | undefined,
): UseSubagentTrackingResult {
  const [subagentTokens, setSubagentTokensRaw] = useState<SubagentTokenMap>(new Map());
  const [taskLiveOutput, setTaskLiveOutput] = useState<Record<string, TaskSessionData>>({});
  const [childSessions, setChildSessions] = useState<ChildSessionReference[]>([]);

  // Wrap the setter in a stable identity that always trims trailing
  // entries past the cap. Stability matters because the new
  // pipeline lists `setSubagentTokens` as an effect dependency in
  // `useSessionStatus`; a fresh identity per render would cause the
  // 1 Hz TPS interval to be torn down + re-armed on every render,
  // and (worse) re-fire the effect synchronously, producing a
  // "Maximum update depth exceeded" warning during active
  // streaming.
  const setSubagentTokens = useCallback<Dispatch<SetStateAction<SubagentTokenMap>>>((next) => {
    setSubagentTokensRaw((prev) => {
      const updated = typeof next === 'function'
        ? (next as (p: SubagentTokenMap) => SubagentTokenMap)(prev)
        : next;
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

  const runningNewSessions = useMemo(() => parts.flatMap((part) => {
    const data = typeof part.data === 'string'
      ? (() => { try { return JSON.parse(part.data); } catch { return null; } })()
      : part.data;
    if (!data || typeof data !== 'object') return [];
    const record = data as Record<string, unknown>;
    const tool = record.tool;
    const state = record.state as Record<string, unknown> | undefined;
    const input = state?.input as Record<string, string> | undefined;
    const isNewSession = tool === 'new_session' || tool === 'mcp_new_session' || tool === 'ocman_new_session';
    return isNewSession && state?.status === 'running' ? [{ id: part.id, intent: input?.intent || '' }] : [];
  }), [parts]);
  const runningNewSessionKey = runningNewSessions.map(({ id, intent }) => `${id}:${intent}`).join(',');

  useEffect(() => {
    if (!sessionId || runningNewSessions.length === 0) return;
    const controller = new AbortController();
    let timer: ReturnType<typeof setTimeout> | undefined;
    const poll = async () => {
      try {
        const response = await api.sessionTasks(sessionId, [], controller.signal);
        if (controller.signal.aborted) return;
        const children = response.children || [];
        setChildSessions((previous) => previous.length === children.length
          && previous.every((child, index) => child.id === children[index]?.id
            && child.status === children[index]?.status)
          ? previous
          : children);
        const unmatched = [...children];
        const allLinked = runningNewSessions.every(({ intent }) => {
          const index = unmatched.findIndex((child) => child.intent === intent);
          if (index < 0) return false;
          unmatched.splice(index, 1);
          return true;
        });
        if (!allLinked) timer = setTimeout(poll, 250);
      } catch {
        if (!controller.signal.aborted) timer = setTimeout(poll, 250);
      }
    };
    poll();
    return () => {
      controller.abort();
      if (timer) clearTimeout(timer);
    };
    // The part IDs make this effect restart when the running calls change.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [runningNewSessionKey, sessionId]);

  // Poll the running tasks for their sub-session data. Effect
  // re-fires whenever the *count* of running tasks changes — using
  // the contents would re-create the interval on every status flip,
  // which costs an extra request without any payoff.
  useEffect(() => {
    if (!sessionId || runningTaskIds.length === 0) return;
    const controller = new AbortController();
    const taskIdList = runningTaskIds.map(({ taskId }) => taskId);
    const poll = async () => {
      if (document.hidden) return;
      try {
        const resp = await api.sessionTasks(sessionId, taskIdList, controller.signal);
        if (controller.signal.aborted) return;
        const tasks = resp.tasks || {};
        const entries = Object.entries(tasks);
        if (entries.length > 0) {
          setTaskLiveOutput((prev) => {
            const next = { ...prev };
            for (const [id, data] of entries) {
              next[id] = data;
            }
            return next;
          });
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

  // Fetch sub-session data for completed tasks that we don't have
  // data for yet. This covers the case where the user navigates
  // away and comes back — the task is completed, the live poll
  // isn't running, but we still need the sub-session messages to
  // render the embedded thread preview.
  const completedTaskIds = useMemo(() => {
    const ids: string[] = [];
    for (const p of parts) {
      const d = typeof p.data === 'string'
        ? (() => { try { return JSON.parse(p.data); } catch { return null; } })()
        : p.data;
      if (!d || typeof d !== 'object') continue;
      const toolName = (d as Record<string, unknown>).tool as string | undefined;
      if (!isTaskTool(toolName)) continue;
      const st = (d as Record<string, unknown>).state as Record<string, unknown> | undefined;
      const status = (st?.status as string) || 'running';
      if (status === 'running') continue; // handled by the poll above
      const taskId = extractTaskId(st);
      if (taskId) ids.push(taskId);
    }
    return ids;
  }, [parts]);

  // Ref to track which completed task ids we've already fetched so
  // we don't re-fetch on every render.
  const fetchedCompletedRef = useRef<Set<string>>(new Set());

  useEffect(() => {
    if (!sessionId || completedTaskIds.length === 0) return;
    // Only fetch tasks we haven't fetched yet.
    const needed = completedTaskIds.filter((id) => !fetchedCompletedRef.current.has(id));
    if (needed.length === 0) return;
    const controller = new AbortController();
    (async () => {
      try {
        const resp = await api.sessionTasks(sessionId, needed, controller.signal);
        if (controller.signal.aborted) return;
        const tasks = resp.tasks || {};
        const entries = Object.entries(tasks);
        if (entries.length > 0) {
          for (const [id] of entries) {
            fetchedCompletedRef.current.add(id);
          }
          setTaskLiveOutput((prev) => {
            const next = { ...prev };
            for (const [id, data] of entries) {
              next[id] = data;
            }
            return next;
          });
        }
      } catch {
        /* ignore — will retry on next parts change */
      }
    })();
    return () => { controller.abort(); };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [completedTaskIds.length, sessionId]);

  return {
    subagentSessionIds,
    subagentSessionIdsRef,
    runningTaskIds,
    subagentTokens,
    setSubagentTokens,
    taskLiveOutput,
    setTaskLiveOutput,
    childSessions,
  };
}
