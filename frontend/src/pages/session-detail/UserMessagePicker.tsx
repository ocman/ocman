import { useMemo } from 'react';
import type { Message, Part, PartData } from '../../lib/api';
import {
  CommandListPicker,
  type PickerEntryBase,
} from '../../components/assistant/CommandListPicker';
import './ForkPicker.css';

const FULL_SESSION = '__full_session__';
const FORK_SECTION = 'Fork session';

interface UserMessageEntry extends PickerEntryBase {
  label: string;
  time: string;
}

// Fork lists the newest prompts first under a "Full session" row; jump lists
// every user message in conversation order.
export type UserMessagePickerProps = {
  open: boolean;
  messages: Message[];
  parts: Part[];
  onClose: () => void;
} & (
  | {
    mode: 'fork';
    /** Offer only the full session (the platform cannot fork at a message). */
    fullSessionOnly?: boolean;
    onSelect: (messageID?: string) => void;
  }
  | { mode: 'jump'; onSelect: (messageID: string) => void }
);

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

export function UserMessagePicker(props: UserMessagePickerProps) {
  const { open, messages, parts, onClose } = props;
  const fork = props.mode === 'fork';
  const fullSessionOnly = props.mode === 'fork' && props.fullSessionOnly;
  const entries = useMemo<UserMessageEntry[]>(() => {
    const full: UserMessageEntry = { value: FULL_SESSION, label: 'Full session', time: '' };
    if (fullSessionOnly) return [full];
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
        label: (partsByMessage.get(message.id) ?? []).join(' ').replace(/\s+/g, ' ').trim() || (fork ? '' : 'Message without text'),
        time: formatTime(message.timeCreated),
      }))
      .filter((entry) => entry.label);

    return fork ? [full, ...prompts.reverse()] : prompts;
  }, [messages, parts, fork, fullSessionOnly]);

  return (
    <CommandListPicker<UserMessageEntry>
      open={open}
      entries={entries}
      fuseKeys={[{ name: 'label', weight: 1 }]}
      sectionOf={fork ? () => FORK_SECTION : undefined}
      sectionOrder={fork ? [FORK_SECTION] : undefined}
      renderRow={(entry) => (
        <>
          <div className="oc-cmd-item-content oc-fork-picker-content">
            <span className="oc-cmd-title">{entry.label}</span>
          </div>
          {entry.time && <span className="oc-fork-picker-time">{entry.time}</span>}
        </>
      )}
      placeholder={(total) => fork ? 'Search user messages' : `Search ${total} user message${total === 1 ? '' : 's'}`}
      emptyMessage={fork ? 'No matching user messages' : 'No user messages'}
      isCurrent={() => false}
      onSelect={(value) => {
        if (props.mode === 'jump') props.onSelect(value);
        else props.onSelect(value === FULL_SESSION ? undefined : value);
      }}
      onClose={onClose}
    />
  );
}
