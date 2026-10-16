import type { parseV2Notification } from '../../lib/v2Notification';
import { TaskNotification } from './TaskNotification';
import { ToolCallDisplay } from './ToolCallDisplay';

const noop = async () => {};

export function V2Notification({ notification }: { notification: NonNullable<ReturnType<typeof parseV2Notification>> }) {
  if (notification.kind === 'shell') {
    const shell = notification.shell;
    return <ToolCallDisplay
      type="tool-call" toolCallId={shell.id} toolName="bash" args={{}}
      argsText={`${shell.status}${shell.userExecuted ? '\n@user-executed-tool' : ''}\n${shell.command}`}
      result={shell.output} status={{ type: 'complete' }}
      addResult={noop} resume={noop} respondToApproval={noop}
    />;
  }
  const task = notification.subagent;
  return <TaskNotification notification={{
    summary: task.description, status: task.status, result: task.output,
    details: [['Session ID', task.sessionID]],
  }} />;
}
