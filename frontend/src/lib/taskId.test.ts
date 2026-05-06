import { describe, it, expect } from 'vitest';
import { extractTaskId, isTaskTool, TASK_TOOL_NAMES } from './taskId';

describe('isTaskTool', () => {
  it('matches the four known task tool names', () => {
    expect(isTaskTool('task')).toBe(true);
    expect(isTaskTool('Task')).toBe(true);
    expect(isTaskTool('mcp_task')).toBe(true);
    expect(isTaskTool('mcp_Task')).toBe(true);
  });

  it('rejects unrelated tool names', () => {
    expect(isTaskTool('read')).toBe(false);
    expect(isTaskTool('TASK')).toBe(false); // case-sensitive on purpose
    expect(isTaskTool('')).toBe(false);
    expect(isTaskTool(undefined)).toBe(false);
    expect(isTaskTool(null)).toBe(false);
  });

  it('exposes the canonical name set as a frozen reference', () => {
    expect(TASK_TOOL_NAMES.size).toBe(4);
    expect(TASK_TOOL_NAMES.has('task')).toBe(true);
  });
});

describe('extractTaskId', () => {
  it('returns the empty string for a missing or empty state', () => {
    expect(extractTaskId(undefined)).toBe('');
    expect(extractTaskId(null)).toBe('');
    expect(extractTaskId({})).toBe('');
  });

  it('prefers the regex on string output over input.task_id (resume-fork case)', () => {
    // The user passed `task_id: ses_input_1` to resume, but the
    // server forked a fresh subagent session and reported it via
    // the streamed output. The renderer must navigate to the live
    // id, not the (now stale) resume hint.
    expect(
      extractTaskId({
        input: { task_id: 'ses_input_1' },
        output: 'task_id: ses_output_1',
      }),
    ).toBe('ses_output_1');
  });

  it('falls back to input.task_id when no output has arrived yet', () => {
    // Early in the streaming lifecycle the output is still empty;
    // the input hint is the only id we have.
    expect(extractTaskId({ input: { task_id: 'ses_input_only' } })).toBe(
      'ses_input_only',
    );
  });

  it('falls back to input.task_id when the output has no task_id line', () => {
    expect(
      extractTaskId({
        input: { task_id: 'ses_input_only' },
        output: 'streaming output without an id marker yet',
      }),
    ).toBe('ses_input_only');
  });

  it('finds task_id in a string output via regex', () => {
    expect(
      extractTaskId({
        output: 'task_id: ses_abc123\nFurther output here',
      }),
    ).toBe('ses_abc123');
  });

  it('finds task_id at end of output without trailing newline', () => {
    expect(extractTaskId({ output: 'task_id: ses_xyz' })).toBe('ses_xyz');
  });

  it('finds task_id inside a structured output object', () => {
    expect(
      extractTaskId({
        output: { task_id: 'ses_obj_1', text: 'irrelevant' },
      }),
    ).toBe('ses_obj_1');
  });

  it('prefers structured output.task_id over input.task_id', () => {
    expect(
      extractTaskId({
        input: { task_id: 'ses_input_stale' },
        output: { task_id: 'ses_obj_live' },
      }),
    ).toBe('ses_obj_live');
  });

  it('falls back to metadata.sessionId', () => {
    expect(
      extractTaskId({
        metadata: { sessionId: 'ses_meta_1' },
      }),
    ).toBe('ses_meta_1');
  });

  it('falls back to metadata.taskId then metadata.task_id', () => {
    expect(extractTaskId({ metadata: { taskId: 'ses_camel' } })).toBe('ses_camel');
    expect(extractTaskId({ metadata: { task_id: 'ses_snake' } })).toBe('ses_snake');
  });

  it('returns the empty string when none of the strategies match', () => {
    expect(
      extractTaskId({
        input: {},
        output: 'no marker here',
        metadata: { other: 'x' },
      }),
    ).toBe('');
  });

  it('ignores empty-string ids at every strategy', () => {
    expect(
      extractTaskId({
        input: { task_id: '' },
        output: { task_id: '' },
        metadata: { sessionId: '', taskId: '', task_id: '' },
      }),
    ).toBe('');
  });

  it('does not match arrays as the output object', () => {
    expect(extractTaskId({ output: [{ task_id: 'ses_arr' }] })).toBe('');
  });
});
