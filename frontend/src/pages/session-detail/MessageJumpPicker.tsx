import { useMemo } from 'react';
import type { Message, Part, PartData } from '../../lib/api';
import { CommandListPicker, type PickerEntryBase } from '../../components/assistant/CommandListPicker';
import './ForkPicker.css';

interface MessageJumpEntry extends PickerEntryBase {
  label: string;
  time: string;
}

export interface MessageJumpPickerProps {
  open: boolean;
  messages: Message[];
  parts: Part[];
  onSelect: (messageId: string) => void;
  onClose: () => void;
}

function partText(part: Part): string {
  try {
    const data = (typeof part.data === 'string' ? JSON.parse(part.data) : part.data) as PartData;
    return data.type === 'text' ? data.text?.trim() ?? '' : '';
  } catch {
    return '';
  }
}

export function MessageJumpPicker({ open, messages, parts, onSelect, onClose }: MessageJumpPickerProps) {
  const entries = useMemo<MessageJumpEntry[]>(() => {
    const textByMessage = new Map<string, string[]>();
    for (const part of parts) {
      const text = partText(part);
      if (!text) continue;
      const list = textByMessage.get(part.messageId) ?? [];
      list.push(text);
      textByMessage.set(part.messageId, list);
    }

    return messages
      .filter((message) => message.data.role === 'user')
      .map((message) => ({
        value: message.id,
        label: (textByMessage.get(message.id) ?? []).join(' ').replace(/\s+/g, ' ').trim() || 'Message without text',
        time: new Date(message.timeCreated).toLocaleTimeString([], { hour: 'numeric', minute: '2-digit' }),
      }));
  }, [messages, parts]);

  return (
    <CommandListPicker<MessageJumpEntry>
      open={open}
      entries={entries}
      fuseKeys={[{ name: 'label', weight: 1 }]}
      renderRow={(entry) => (
        <>
          <div className="oc-cmd-item-content oc-fork-picker-content">
            <span className="oc-cmd-title">{entry.label}</span>
          </div>
          <span className="oc-fork-picker-time">{entry.time}</span>
        </>
      )}
      placeholder={(total) => `Search ${total} user message${total === 1 ? '' : 's'}`}
      emptyMessage="No user messages"
      isCurrent={() => false}
      onSelect={onSelect}
      onClose={onClose}
    />
  );
}
