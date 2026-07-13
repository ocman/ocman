import { useMemo } from 'react';
import type { Message, Part, PartData } from '../../lib/api';
import {
  CommandListPicker,
  type PickerEntryBase,
} from '../../components/assistant/CommandListPicker';
import './ForkPicker.css';

const FULL_SESSION = '__full_session__';

interface ForkEntry extends PickerEntryBase {
  label: string;
  time: string;
}

export interface ForkPickerProps {
  open: boolean;
  messages: Message[];
  parts: Part[];
  onSelect: (messageID?: string) => void;
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

function formatTime(timestamp: number): string {
  return new Date(timestamp).toLocaleTimeString([], { hour: 'numeric', minute: '2-digit' });
}

export function ForkPicker({ open, messages, parts, onSelect, onClose }: ForkPickerProps) {
  const entries = useMemo<ForkEntry[]>(() => {
    const partsByMessage = new Map<string, string[]>();
    for (const part of parts) {
      const text = partText(part);
      if (!text) continue;
      const list = partsByMessage.get(part.messageId) ?? [];
      list.push(text);
      partsByMessage.set(part.messageId, list);
    }

    const prompts = messages
      .filter((message) => message.data.role === 'user')
      .map((message) => ({
        value: message.id,
        label: (partsByMessage.get(message.id) ?? []).join(' ').replace(/\s+/g, ' ').trim(),
        time: formatTime(message.timeCreated),
      }))
      .filter((entry) => entry.label)
      .reverse();

    return [{ value: FULL_SESSION, label: 'Full session', time: '' }, ...prompts];
  }, [messages, parts]);

  return (
    <CommandListPicker<ForkEntry>
      open={open}
      entries={entries}
      fuseKeys={[{ name: 'label', weight: 1 }]}
      sectionOf={() => 'Fork session'}
      sectionOrder={['Fork session']}
      renderRow={(entry) => (
        <>
          <div className="oc-cmd-item-content oc-fork-picker-content">
            <span className="oc-cmd-title">{entry.label}</span>
          </div>
          {entry.time && <span className="oc-fork-picker-time">{entry.time}</span>}
        </>
      )}
      placeholder={() => 'Search user messages'}
      emptyMessage="No matching user messages"
      isCurrent={() => false}
      onSelect={(value) => onSelect(value === FULL_SESSION ? undefined : value)}
      onClose={onClose}
    />
  );
}
