import { useEffect, useMemo, useRef, useState } from 'react';
import type { Dispatch, SetStateAction } from 'react';
import type { Part, TaskSessionData } from '../../lib/api';
import { api } from '../../lib/api';
import { parsePart } from '../../lib/convertMessages';
import { extractTaskId, isTaskTool } from '../../lib/taskId';

/**
 * Merge fetched task data into the cache, returning `prev` when nothing
 * changed. taskLiveOutput is part of convertMessages' per-message cache
 * key, so a fresh object per poll would re-convert every message.
 */
function mergeTaskData(
  prev: Record<string, TaskSessionData>,
  entries: [string, TaskSessionData][],
): Record<string, TaskSessionData> {
  let next: Record<string, TaskSessionData> | null = null;
  for (const [id, data] of entries) {
    // ponytail: JSON compare, O(sub-session size) every 2s; fine at 150-200 messages.
    if (prev[id] && JSON.stringify(prev[id]) === JSON.stringify(data)) continue;
    next ??= { ...prev };
    next[id] = data;
  }
  return next ?? prev;
}

/** One row of an in-flight task's status. */
export interface RunningTaskEntry {
  taskId: string;
  status: string;
}

export interface UseSubagentTrackingResult {
  /** In-flight task entries, derived from task-tool parts. */
  runningTaskIds: RunningTaskEntry[];
  /** Sub-session data per subagent task, keyed by task id. */
  taskLiveOutput: Record<string, TaskSessionData>;
  setTaskLiveOutput: Dispatch<SetStateAction<Record<string, TaskSessionData>>>;
}

/**
 * Track subagent (`task` / `mcp_task`) progress for a session.
 *
 * Owns the sub-session data cache per task, fed to the runtime
 * provider so the assistant thread can render an embedded thread
 * preview of the subagent conversation.
 *
 * The hook runs a 2-second poll against /api/session/{id}/tasks for
 * running child sessions, and a one-shot fetch for completed launch calls.
 * A completed launch can still have a busy child, which continues polling.
 */
export function useSubagentTracking(
  parts: Part[],
  sessionId: string | undefined,
): UseSubagentTrackingResult {
  const [taskLiveOutput, setTaskLiveOutput] = useState<Record<string, TaskSessionData>>({});
  const fetchedCompletedRef = useRef<Set<string>>(new Set());

  useEffect(() => {
    const reset = () => {
      setTaskLiveOutput({});
      fetchedCompletedRef.current.clear();
    };
    reset();
  }, [sessionId]);

  // Keep tracking while either the launch call or its child is running.
  // A resumed child can still report its previous settled turn initially.
  const { runningTaskIds, completedTaskIds } = useMemo(() => {
    const running: RunningTaskEntry[] = [];
    const completed: string[] = [];
    for (const p of parts) {
      const d = parsePart(p);
      if (!d || typeof d !== 'object') continue;
      if (!isTaskTool(d.tool)) continue;
      const st = d.state as Record<string, unknown> | undefined;
      const taskId = extractTaskId(st);
      if (!taskId) continue;
      const childStatus = taskLiveOutput[taskId]?.status;
      const status = childStatus === 'busy' ? 'running' : (st?.status as string) || 'running';
      if (status === 'running') running.push({ taskId, status });
      else completed.push(taskId);
    }
    return { runningTaskIds: running, completedTaskIds: completed };
  }, [parts, taskLiveOutput]);

  const runningTaskKey = runningTaskIds.map(({ taskId }) => taskId).join('\0');

  // Poll the running tasks for their sub-session data. Restart when the
  // identity set changes so a same-size replacement never polls stale IDs.
  useEffect(() => {
    if (!sessionId || runningTaskIds.length === 0) return;
    const controller = new AbortController();
    const taskIdList = runningTaskIds.map(({ taskId }) => taskId);
    let inFlight = false;
    const poll = async () => {
      if (document.hidden || inFlight) return;
      inFlight = true;
      try {
        const resp = await api.sessionTasks(sessionId, taskIdList, controller.signal);
        if (controller.signal.aborted) return;
        const tasks = resp.tasks || {};
        const entries = Object.entries(tasks);
        if (entries.length > 0) {
          setTaskLiveOutput((prev) => mergeTaskData(prev, entries));
        }
      } catch {
        /* ignore poll errors — next tick retries */
      } finally {
        inFlight = false;
      }
    };
    poll();
    const interval = setInterval(poll, 2000);
    return () => {
      controller.abort();
      clearInterval(interval);
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [runningTaskKey, sessionId]);

  // Fetch sub-session data for completed tasks that we don't have
  // data for yet. This covers the case where the user navigates
  // away and comes back — the task is completed, the live poll
  // isn't running, but we still need the sub-session messages to
  // render the embedded thread preview.
  const completedTaskKey = completedTaskIds.join('\0');

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
          setTaskLiveOutput((prev) => mergeTaskData(prev, entries));
        }
      } catch {
        /* ignore — will retry on next parts change */
      }
    })();
    return () => { controller.abort(); };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [completedTaskKey, sessionId]);

  return {
    runningTaskIds,
    taskLiveOutput,
    setTaskLiveOutput,
  };
}
