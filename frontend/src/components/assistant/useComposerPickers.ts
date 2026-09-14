import { useCallback, useState } from 'react';
import type { MutableRefObject, RefObject } from 'react';
import type { AgentInfo } from '../../lib/api';

export interface UseComposerPickersOptions {
  inputRef: RefObject<HTMLTextAreaElement | null>;
  sessionIdRef: MutableRefObject<string | undefined>;
  scheduleDraftSave: (sessionId: string, read: () => string) => void;
  models: string[] | undefined;
  agents: AgentInfo[] | undefined;
  agentOptions: string[];
  onModelChange?: (model: string) => void;
  onAgentChange?: (agent: string) => void;
  onRefreshModels?: () => void;
}

/**
 * Open/query state for the composer's modal pickers plus the
 * `/model <arg>` / `/agent <arg>` shortcuts that apply a unique match
 * directly instead of opening the palette.
 */
export function useComposerPickers({
  inputRef,
  sessionIdRef,
  scheduleDraftSave,
  models,
  agents,
  agentOptions,
  onModelChange,
  onAgentChange,
  onRefreshModels,
}: UseComposerPickersOptions) {
  const [modelPickerOpen, setModelPickerOpen] = useState(false);
  const [modelPickerQuery, setModelPickerQuery] = useState('');
  const [agentPickerOpen, setAgentPickerOpen] = useState(false);
  const [agentPickerQuery, setAgentPickerQuery] = useState('');
  const [skillPickerOpen, setSkillPickerOpen] = useState(false);
  const [skillPickerQuery, setSkillPickerQuery] = useState('');
  const [routinePickerOpen, setRoutinePickerOpen] = useState(false);
  const [routinePickerQuery, setRoutinePickerQuery] = useState('');
  const [reasoningPickerOpen, setReasoningPickerOpen] = useState(false);
  const [helpOpen, setHelpOpen] = useState(false);

  // Try to resolve `args` to a concrete model without opening the palette.
  // Matches against the full `provider/model` string or the bare model name,
  // case-insensitively. Returns the resolved value if there's exactly one match.
  const resolveModelArg = useCallback((arg: string): string | null => {
    const list = models || [];
    if (!arg) return null;
    const q = arg.toLowerCase();
    const exact = list.find((m) => m.toLowerCase() === q);
    if (exact) return exact;
    const byModelName = list.filter((m) => {
      const idx = m.indexOf('/');
      const name = idx > 0 ? m.slice(idx + 1) : m;
      return name.toLowerCase() === q;
    });
    if (byModelName.length === 1) return byModelName[0];
    return null;
  }, [models]);

  // Open the /model palette. If `arg` uniquely identifies a model, apply it
  // directly instead of opening the modal. The arg is otherwise pre-filled
  // as the palette's initial query.
  const openModelPicker = useCallback((arg = '') => {
    const resolved = resolveModelArg(arg);
    if (resolved) {
      onModelChange?.(resolved);
      return;
    }
    // Fire-and-forget: pull the latest provider catalog. The picker opens
    // with current data; the refresh flows in via a `modelEntries` prop
    // update on the next render.
    onRefreshModels?.();
    setModelPickerQuery(arg);
    setModelPickerOpen(true);
  }, [resolveModelArg, onModelChange, onRefreshModels]);

  // Same shape as resolveModelArg: case-insensitive exact match against
  // known agent names, so `/agent plan` applies without opening the palette.
  const resolveAgentArg = useCallback((arg: string): string | null => {
    if (!arg) return null;
    const q = arg.toLowerCase();
    const names = new Set<string>();
    for (const a of agents || []) names.add(a.name);
    for (const n of agentOptions) names.add(n);
    for (const n of names) {
      if (n.toLowerCase() === q) return n;
    }
    return null;
  }, [agents, agentOptions]);

  const openAgentPicker = useCallback((arg = '') => {
    const resolved = resolveAgentArg(arg);
    if (resolved) {
      onAgentChange?.(resolved);
      return;
    }
    setAgentPickerQuery(arg);
    setAgentPickerOpen(true);
  }, [resolveAgentArg, onAgentChange]);

  const openSkillPicker = useCallback((arg: string) => {
    setSkillPickerQuery(arg);
    setSkillPickerOpen(true);
  }, []);

  const openRoutinePicker = useCallback((arg: string) => {
    setRoutinePickerQuery(arg);
    setRoutinePickerOpen(true);
  }, []);

  /** Put `text` in the textarea, focus it, and persist as the draft. */
  const insertText = useCallback((text: string) => {
    const el = inputRef.current;
    if (!el) return;
    el.value = text;
    el.focus();
    const sid = sessionIdRef.current;
    if (sid) scheduleDraftSave(sid, () => el.value);
  }, [inputRef, sessionIdRef, scheduleDraftSave]);

  // On skill select: prefill `/<skill> ` into the composer — do not send.
  // Matches OpenCode's DialogSkill, which inserts the command for the user
  // to complete/submit themselves.
  const insertSkill = useCallback((skill: string) => {
    setSkillPickerOpen(false);
    insertText('/' + skill + ' ');
  }, [insertText]);

  const insertRoutine = useCallback((routine: { prompt: string }) => {
    setRoutinePickerOpen(false);
    insertText(routine.prompt);
  }, [insertText]);

  /** Close the picker and hand focus back to the textarea. */
  const closeAndFocus = useCallback((setOpen: (v: boolean) => void) => () => {
    setOpen(false);
    inputRef.current?.focus();
  }, [inputRef]);

  return {
    model: { open: modelPickerOpen, query: modelPickerQuery, close: closeAndFocus(setModelPickerOpen), setOpen: setModelPickerOpen },
    agent: { open: agentPickerOpen, query: agentPickerQuery, close: closeAndFocus(setAgentPickerOpen) },
    skill: { open: skillPickerOpen, query: skillPickerQuery, close: closeAndFocus(setSkillPickerOpen) },
    routine: { open: routinePickerOpen, query: routinePickerQuery, close: closeAndFocus(setRoutinePickerOpen) },
    reasoning: { open: reasoningPickerOpen, close: closeAndFocus(setReasoningPickerOpen), setOpen: setReasoningPickerOpen },
    help: { open: helpOpen, close: closeAndFocus(setHelpOpen), setOpen: setHelpOpen },
    openModelPicker,
    openAgentPicker,
    openSkillPicker,
    openRoutinePicker,
    insertSkill,
    insertRoutine,
  };
}
