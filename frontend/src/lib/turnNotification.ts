import type { Message, PartData } from './api';
import { parseTaskNotification } from './taskNotification';
import { parseV2Notification } from './v2Notification';

/** A background shell result, not a shell command explicitly run by the user. */
export function isBackgroundShellNotification(parts: PartData[]): boolean {
  const content = parts.filter((part) => part.type !== 'step-start' && part.type !== 'step-finish');
  if (content.length !== 1 || content[0].type !== 'text') return false;
  const notification = parseV2Notification(content[0].text ?? '');
  return notification?.kind === 'shell' && !notification.shell.userExecuted
    && (notification.shell.status === 'completed' || notification.shell.status === 'error');
}

export function isCompletedReply(message: Message | undefined): boolean {
  return message?.data.role === 'assistant' && !message.data.error && !message.data.summary && message.data.agent !== 'compaction'
    && (message.data.finish === 'stop' || message.data.finish === 'end_turn');
}

/** Injected user-role context continues a turn; it isn't a new human prompt. */
export function isTurnNotification(message: Message, parts: PartData[]): boolean {
  if (message.data.synthetic) return true;
  const content = parts.filter((part) => part.type !== 'step-start' && part.type !== 'step-finish');
  return content.length > 0 && content.every((part) => part.type === 'text'
    && (part.synthetic || !!parseV2Notification(part.text ?? '') || !!parseTaskNotification(part.text ?? '')));
}
