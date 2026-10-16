import type { Message, PartData } from './api';
import { parseTaskNotification } from './taskNotification';
import { parseV2Notification } from './v2Notification';

/** Injected user-role context continues a turn; it isn't a new human prompt. */
export function isTurnNotification(message: Message, parts: PartData[]): boolean {
  if (message.data.synthetic) return true;
  const content = parts.filter((part) => part.type !== 'step-start' && part.type !== 'step-finish');
  return content.length > 0 && content.every((part) => part.type === 'text'
    && (part.synthetic || !!parseV2Notification(part.text ?? '') || !!parseTaskNotification(part.text ?? '')));
}
