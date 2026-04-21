import { useState, useEffect, useRef, useCallback, useMemo, memo } from 'react';
import { createPortal } from 'react-dom';
import './Composer.css';
import { getDraft, saveDraft, clearDraft } from '../../lib/composerDraft';
import { isMacPlatform } from '../../lib/shortcuts';
import { useShortcut } from '../../lib/shortcutRegistry';
import { useUiStore } from '../../lib/uiStore';
import { useApiStore } from '../../lib/apiStore';
import { api, type SlashCommand, type AgentInfo, type SessionModelEntry } from '../../lib/api';
import { agentColor } from '../../lib/agentColor';
import { ModelPicker } from './ModelPicker';
import { AgentPicker } from './AgentPicker';
import { ReasoningPicker } from './ReasoningPicker';

function encodeWav(samples: Float32Array, sampleRate: number): Blob {
  const numSamples = samples.length;
  const buffer = new ArrayBuffer(44 + numSamples * 2);
  const view = new DataView(buffer);

  function writeString(offset: number, str: string) {
    for (let i = 0; i < str.length; i++) view.setUint8(offset + i, str.charCodeAt(i));
  }

  writeString(0, 'RIFF');
  view.setUint32(4, 36 + numSamples * 2, true);
  writeString(8, 'WAVE');
  writeString(12, 'fmt ');
  view.setUint32(16, 16, true);
  view.setUint16(20, 1, true);
  view.setUint16(22, 1, true);
  view.setUint32(24, sampleRate, true);
  view.setUint32(28, sampleRate * 2, true);
  view.setUint16(32, 2, true);
  view.setUint16(34, 16, true);
  writeString(36, 'data');
  view.setUint32(40, numSamples * 2, true);

  let offset = 44;
  for (let i = 0; i < numSamples; i++, offset += 2) {
    const s = Math.max(-1, Math.min(1, samples[i]));
    view.setInt16(offset, s < 0 ? s * 0x8000 : s * 0x7FFF, true);
  }

  return new Blob([buffer], { type: 'audio/wav' });
}

interface RecordingCtx {
  stream: MediaStream;
  audioCtx: AudioContext;
  processor: ScriptProcessorNode;
  chunks: Float32Array[];
}

export interface AttachedImage {
  url: string;
  mime: string;
}

function readFileAsDataURL(file: File): Promise<string> {
  return new Promise((resolve, reject) => {
    const reader = new FileReader();
    reader.onload = () => resolve(reader.result as string);
    reader.onerror = reject;
    reader.readAsDataURL(file);
  });
}

const MODEL_CONTEXT_WINDOWS: Record<string, number> = {
  'gpt-4o': 128_000,
  'gpt-4o-mini': 128_000,
  'gpt-4-turbo': 128_000,
  'gpt-4': 8_192,
  'gpt-4.1': 1_047_576,
  'gpt-4.1-mini': 1_047_576,
  'gpt-4.1-nano': 1_047_576,
  'o1': 200_000,
  'o1-mini': 128_000,
  'o1-pro': 200_000,
  'o3': 200_000,
  'o3-mini': 200_000,
  'o3-pro': 200_000,
  'o4-mini': 200_000,
  'claude-sonnet-4-20250514': 200_000,
  'claude-opus-4-20250514': 200_000,
  'claude-opus-4-6-20250616': 200_000,
  'claude-3-7-sonnet-20250219': 200_000,
  'claude-3-5-sonnet-20241022': 200_000,
  'claude-3-5-sonnet-20240620': 200_000,
  'claude-3-5-haiku-20241022': 200_000,
  'claude-3-opus-20240229': 200_000,
  'claude-3-haiku-20240307': 200_000,
  'claude-sonnet-4': 200_000,
  'claude-opus-4-6': 200_000,
  'claude-opus-4': 200_000,
  'gemini-2.5-pro': 1_048_576,
  'gemini-2.5-flash': 1_048_576,
  'gemini-2.0-flash': 1_048_576,
  'gemini-1.5-pro': 2_097_152,
  'gemini-1.5-flash': 1_048_576,
  'grok-3': 131_072,
  'grok-3-mini': 131_072,
  'deepseek-chat': 64_000,
  'deepseek-reasoner': 64_000,
  'mistral-large-latest': 128_000,
  'codestral-latest': 256_000,
};

function getContextWindow(modelId: string | undefined): number | null {
  if (!modelId) return null;
  if (MODEL_CONTEXT_WINDOWS[modelId]) return MODEL_CONTEXT_WINDOWS[modelId];
  const lower = modelId.toLowerCase();
  const sorted = Object.entries(MODEL_CONTEXT_WINDOWS).sort((a, b) => b[0].length - a[0].length);
  for (const [key, value] of sorted) {
    if (lower.endsWith(key) || lower.includes(key)) return value;
  }
  return null;
}

function formatTokenCount(n: number): string {
  if (n >= 1e6) return (n / 1e6).toFixed(1) + 'M';
  if (n >= 1e3) return (n / 1e3).toFixed(1) + 'K';
  return String(n);
}

const KNOWN_AGENTS = ['build', 'developer', 'plan', 'architect', 'ba', 'brainstormer', 'reviewer', 'security'];

const BUILTIN_COMMANDS: SlashCommand[] = [
  { name: 'agent', description: 'Change the active agent (opens a picker)' },
  { name: 'archive', description: 'Archive this session and open the most recent one' },
  { name: 'compact', description: 'Summarize conversation history to free up context window' },
  { name: 'model', description: 'Change the active model (opens a picker)' },
  { name: 'new', description: 'Start a new session in the same project directory (optionally add a title)' },
  { name: 'rename', description: 'Rename this session' },
  { name: 'tmux', description: 'Switch to the tmux session for this project' },
  { name: 'vscode', description: 'Open the project directory in VS Code' },
];

function ComposerImpl({
  onSend,
  onCommand,
  onAbort,
  isRunning,
  disabled,
  whisperAvailable,
  models,
  modelEntries,
  activeModel,
  selectedModel,
  onModelChange,
  activeAgent,
  selectedAgent,
  onAgentChange,
  agents,
  agentsLoaded,
  contextTokens,
  sessionId,
  tokensPerSecond,
  tokenStats,
  selectedReasoning,
  onReasoningChange,
  disabledHint,
}: {
  onSend?: (text: string, images?: AttachedImage[]) => void;
  onCommand?: (command: string, args: string) => void;
  onAbort?: () => void;
  isRunning: boolean;
  disabled?: boolean;
  /**
   * User-facing hint explaining *why* the composer is disabled and
   * how to fix it (e.g. "Start OpenCode with `opencode --port 0` ..."
   * for OpenCode). Shown as the textarea placeholder when disabled.
   * Falls back to a generic "No live connection" message.
   */
  disabledHint?: string;
  whisperAvailable?: boolean;
  models?: string[];
  modelEntries?: SessionModelEntry[];
  activeModel?: string;
  selectedModel?: string;
  onModelChange?: (model: string) => void;
  activeAgent?: string;
  selectedAgent?: string;
  onAgentChange?: (agent: string) => void;
  agents?: AgentInfo[];
  /**
   * Whether the /agent catalog has finished loading (success or failure) for
   * the current session directory. When false, the composer intentionally
   * stays muted — it avoids applying an agent-derived accent color that might
   * change seconds later once the authoritative colors resolve (which
   * manifested as a pink flash on page load).
   */
  agentsLoaded?: boolean;
  contextTokens?: number;
  sessionId?: string;
  tokensPerSecond?: number;
  tokenStats?: {
    input: number;
    output: number;
    reasoning: number;
    cacheRead: number;
    cacheWrite: number;
    totalCost: number;
    contextWindow?: number;
  };
  selectedReasoning?: string;
  onReasoningChange?: (reasoning: string) => void;
}) {
  const [showTokenPopover, setShowTokenPopover] = useState(false);
  const [estCost, setEstCost] = useState<{ cost: number; known: boolean } | null>(null);
  const [estCostLoading, setEstCostLoading] = useState(false);
  const tokenPopoverRef = useRef<HTMLDivElement>(null);
  const openShortcuts = useUiStore((s) => s.openShortcuts);

  const wrapRef = useRef<HTMLDivElement>(null);
  const inputRef = useRef<HTMLTextAreaElement>(null);
  const micRef = useRef<HTMLButtonElement>(null);
  const fileInputRef = useRef<HTMLInputElement>(null);
  const onSendRef = useRef(onSend);
  const isRunningRef = useRef(isRunning);
  const disabledRef = useRef(disabled);
  const recordingRef = useRef<RecordingCtx | null>(null);
  const [isRecording, setIsRecording] = useState(false);
  const [images, setImages] = useState<AttachedImage[]>([]);
  const imagesRef = useRef<AttachedImage[]>([]);
  const sessionIdRef = useRef(sessionId);
  const draftTimerRef = useRef<ReturnType<typeof setTimeout> | null>(null);
  const onAbortRef = useRef(onAbort);

  useEffect(() => { imagesRef.current = images; }, [images]);
  useEffect(() => { sessionIdRef.current = sessionId; }, [sessionId]);
  useEffect(() => { onSendRef.current = onSend; }, [onSend]);
  useEffect(() => { onAbortRef.current = onAbort; }, [onAbort]);
  useEffect(() => { isRunningRef.current = isRunning; }, [isRunning]);
  useEffect(() => { disabledRef.current = disabled; }, [disabled]);

  // Auto-focus the composer input when the component becomes visible.
  useEffect(() => {
    if (!disabled && inputRef.current) {
      const timer = setTimeout(() => {
        if (document.activeElement?.tagName !== 'INPUT' && document.activeElement?.tagName !== 'TEXTAREA') {
          inputRef.current?.focus();
        }
      }, 0);
      return () => clearTimeout(timer);
    }
  }, [disabled]);

  const onCommandRef = useRef(onCommand);
  useEffect(() => { onCommandRef.current = onCommand; }, [onCommand]);
  const agentOptionsRef = useRef<string[]>([]);
  const effectiveAgentRef = useRef<string>('');
  const onAgentChangeRef = useRef(onAgentChange);
  useEffect(() => { onAgentChangeRef.current = onAgentChange; }, [onAgentChange]);
  const [slashCommands, setSlashCommands] = useState<SlashCommand[]>([]);
  const [showSlashMenu, setShowSlashMenu] = useState(false);
  const [slashFilter, setSlashFilter] = useState('');
  const [slashIndex, setSlashIndex] = useState(0);
  const [isBashMode, setIsBashMode] = useState(false);
  const setIsBashModeRef = useRef(setIsBashMode);
  useEffect(() => { setIsBashModeRef.current = setIsBashMode; }, []);
  const [modelPickerOpen, setModelPickerOpen] = useState(false);
  const [modelPickerQuery, setModelPickerQuery] = useState('');
  const [agentPickerOpen, setAgentPickerOpen] = useState(false);
  const [agentPickerQuery, setAgentPickerQuery] = useState('');
  const [reasoningPickerOpen, setReasoningPickerOpen] = useState(false);
  // Refs let the non-React keydown/submit handlers (which capture their
  // initial closure) always see the latest lists + callbacks without having
  // to re-bind every render.
  const modelsRef = useRef<string[] | undefined>(models);
  const onModelChangeRef = useRef(onModelChange);
  const agentsRef = useRef<AgentInfo[] | undefined>(agents);
  useEffect(() => { modelsRef.current = models; }, [models]);
  useEffect(() => { onModelChangeRef.current = onModelChange; }, [onModelChange]);
  useEffect(() => { agentsRef.current = agents; }, [agents]);
  const slashMenuRef = useRef<HTMLDivElement>(null);
  const showSlashMenuRef = useRef(false);
  const slashIndexRef = useRef(0);
  const filteredCommandsRef = useRef<SlashCommand[]>([]);

  useEffect(() => {
    if (!sessionId) return;
    let cancelled = false;
    api.commands(sessionId).then(cmds => {
      if (!cancelled) {
        const fetched = cmds || [];
        const merged = [
          ...BUILTIN_COMMANDS.filter(b => !fetched.some(f => f.name === b.name)),
          ...fetched,
        ];
        setSlashCommands(merged);
      }
    }).catch(() => {
      setSlashCommands(BUILTIN_COMMANDS);
    });
    return () => { cancelled = true; };
  }, [sessionId]);

  const hasModels = !!((models && models.length > 0) || (modelEntries && modelEntries.length > 0));
  // Agent picker has something to show as long as we know *any* agent name —
  // either from OpenCode's /agent catalog or the always-present KNOWN_AGENTS
  // fallback in agentOptionsRef.
  const hasAgents = !!(agents && agents.length > 0);
  const filteredCommands = slashCommands.filter(cmd => {
    if (cmd.name === 'model' && !hasModels) return false;
    if (cmd.name === 'agent' && !hasAgents && (!activeAgent)) return false;
    return cmd.name.toLowerCase().startsWith(slashFilter.toLowerCase());
  });

  useEffect(() => { showSlashMenuRef.current = showSlashMenu; }, [showSlashMenu]);
  useEffect(() => { slashIndexRef.current = slashIndex; }, [slashIndex]);
  useEffect(() => { filteredCommandsRef.current = filteredCommands; }, [filteredCommands]);

  useEffect(() => {
    if (!showSlashMenu || !slashMenuRef.current) return;
    const active = slashMenuRef.current.querySelector('.oc-slash-item.active');
    if (active) active.scrollIntoView({ block: 'nearest' });
  }, [slashIndex, showSlashMenu]);

  // Try to resolve `args` to a concrete model without opening the palette.
  // Matches against the full `provider/model` string or the bare model name,
  // case-insensitively. Returns the resolved value if there's exactly one match.
  const resolveModelArg = useCallback((arg: string): string | null => {
    const list = modelsRef.current || [];
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
  }, []);

  // Open the /model palette. If `arg` uniquely identifies a model, apply it
  // directly instead of opening the modal. The arg is otherwise pre-filled
  // as the palette's initial query.
  const openModelPicker = useCallback((arg: string) => {
    const resolved = resolveModelArg(arg);
    if (resolved) {
      onModelChangeRef.current?.(resolved);
      return;
    }
    setModelPickerQuery(arg);
    setModelPickerOpen(true);
  }, [resolveModelArg]);

  // Same shape as resolveModelArg: case-insensitive exact match against
  // known agent names, so `/agent plan` applies without opening the palette.
  const resolveAgentArg = useCallback((arg: string): string | null => {
    if (!arg) return null;
    const q = arg.toLowerCase();
    const names = new Set<string>();
    for (const a of agentsRef.current || []) names.add(a.name);
    for (const n of agentOptionsRef.current || []) names.add(n);
    for (const n of names) {
      if (n.toLowerCase() === q) return n;
    }
    return null;
  }, []);

  const openAgentPicker = useCallback((arg: string) => {
    const resolved = resolveAgentArg(arg);
    if (resolved) {
      onAgentChangeRef.current?.(resolved);
      return;
    }
    setAgentPickerQuery(arg);
    setAgentPickerOpen(true);
  }, [resolveAgentArg]);

  const clearComposerInput = useCallback(() => {
    const el = inputRef.current;
    if (!el) return;
    el.value = '';
    el.style.height = 'auto';
    setShowSlashMenu(false);
    setSlashFilter('');
    setSlashIndex(0);
    const sid = sessionIdRef.current;
    if (sid) {
      if (draftTimerRef.current) clearTimeout(draftTimerRef.current);
      clearDraft(sid);
    }
  }, []);

  const selectSlashCommand = useCallback((cmd: SlashCommand) => {
    const el = inputRef.current;
    if (!el) return;
    // /model and /agent are client-only commands — picking them from the
    // slash menu opens their picker instead of inserting command text.
    if (cmd.name === 'model') {
      clearComposerInput();
      openModelPicker('');
      return;
    }
    if (cmd.name === 'agent') {
      clearComposerInput();
      openAgentPicker('');
      return;
    }
    el.value = '/' + cmd.name + ' ';
    el.style.height = 'auto';
    el.style.height = Math.min(el.scrollHeight, 200) + 'px';
    el.focus();
    setShowSlashMenu(false);
    setSlashFilter('');
    setSlashIndex(0);
  }, [clearComposerInput, openModelPicker, openAgentPicker]);

  useEffect(() => {
    const el = inputRef.current;
    if (!el || !sessionId) return;
    const draft = getDraft(sessionId);
    el.value = draft;
    el.style.height = 'auto';
    if (draft) {
      el.style.height = Math.min(el.scrollHeight, 200) + 'px';
    }
    el.focus();
  }, [sessionId]);

  useEffect(() => {
    const el = inputRef.current;
    return () => {
      if (draftTimerRef.current) {
        clearTimeout(draftTimerRef.current);
        draftTimerRef.current = null;
      }
      const sid = sessionIdRef.current;
      if (el && sid) {
        const text = el.value.trim();
        if (text) {
          saveDraft(sid, text);
        } else {
          clearDraft(sid);
        }
      }
    };
  }, []);

  const addImageFiles = useCallback(async (files: File[]) => {
    const imageFiles = files.filter(f => f.type.startsWith('image/'));
    if (imageFiles.length === 0) return;
    const newImages: AttachedImage[] = [];
    for (const file of imageFiles) {
      try {
        const url = await readFileAsDataURL(file);
        newImages.push({ url, mime: file.type });
      } catch (err) {
        console.error('Failed to read image', err);
      }
    }
    setImages(prev => [...prev, ...newImages]);
  }, []);

  const removeImage = useCallback((index: number) => {
    setImages(prev => prev.filter((_, i) => i !== index));
  }, []);

  useEffect(() => {
    const el = inputRef.current;
    if (!el) return;
    el.disabled = !!disabled;
  }, [disabled]);

  const setMicState = useCallback((state: 'idle' | 'recording' | 'transcribing') => {
    setIsRecording(state === 'recording');
    const btn = micRef.current;
    if (!btn) return;
    const icon = btn.querySelector('.oc-mic-icon');
    if (!(icon instanceof HTMLElement)) return;
    btn.classList.remove('oc-mic-recording', 'oc-mic-transcribing');
    btn.disabled = state === 'transcribing' || !!disabledRef.current;
    icon.className = 'bi oc-mic-icon';
    if (state === 'recording') {
      btn.classList.add('oc-mic-recording');
      icon.classList.add('bi-stop-fill');
    } else if (state === 'transcribing') {
      btn.classList.add('oc-mic-transcribing');
      icon.classList.add('bi-hourglass-split');
    } else {
      icon.classList.add('bi-mic-fill');
    }
  }, []);
  const transcribe = useApiStore((state) => state.transcribe);

  const stopRecording = useCallback((): Blob | null => {
    const ctx = recordingRef.current;
    if (!ctx) return null;
    recordingRef.current = null;

    ctx.processor.disconnect();
    ctx.stream.getTracks().forEach(t => t.stop());

    const totalLen = ctx.chunks.reduce((sum, c) => sum + c.length, 0);
    const merged = new Float32Array(totalLen);
    let offset = 0;
    for (const chunk of ctx.chunks) {
      merged.set(chunk, offset);
      offset += chunk.length;
    }

    const origRate = ctx.audioCtx.sampleRate;
    ctx.audioCtx.close();

    let samples = merged;
    if (origRate !== 16000) {
      const ratio = origRate / 16000;
      const newLen = Math.floor(merged.length / ratio);
      const downsampled = new Float32Array(newLen);
      for (let i = 0; i < newLen; i++) {
        downsampled[i] = merged[Math.floor(i * ratio)];
      }
      samples = downsampled;
    }

    return encodeWav(samples, 16000);
  }, []);

  const submitRecording = useCallback(async () => {
    if (!recordingRef.current) return;
    setMicState('transcribing');
    const blob = stopRecording();
    if (blob && blob.size > 44) {
      try {
        const text = await transcribe(blob);
        if (text && inputRef.current) {
          inputRef.current.value += (inputRef.current.value ? ' ' : '') + text;
          inputRef.current.dispatchEvent(new Event('input'));
          inputRef.current.focus();
        }
      } catch (err) {
        console.error('Transcription failed', err);
      }
    }
    setMicState('idle');
  }, [setMicState, stopRecording, transcribe]);

  const cancelRecording = useCallback(() => {
    if (!recordingRef.current) return;
    const ctx = recordingRef.current;
    recordingRef.current = null;
    ctx.processor.disconnect();
    ctx.stream.getTracks().forEach(t => t.stop());
    ctx.audioCtx.close();
    setMicState('idle');
  }, [setMicState]);

  const handleMicClick = useCallback(async () => {
    if (disabledRef.current) return;

    if (recordingRef.current) {
      await submitRecording();
      return;
    }

    try {
      if (!navigator.mediaDevices?.getUserMedia) {
        console.error('getUserMedia not supported');
        return;
      }
      const stream = await navigator.mediaDevices.getUserMedia({ audio: true });
      const audioCtx = new (window.AudioContext || (window as unknown as { webkitAudioContext: typeof AudioContext }).webkitAudioContext)();
      const source = audioCtx.createMediaStreamSource(stream);
      const processor = audioCtx.createScriptProcessor(4096, 1, 1);
      const chunks: Float32Array[] = [];

      processor.onaudioprocess = (e) => {
        const data = e.inputBuffer.getChannelData(0);
        chunks.push(new Float32Array(data));
      };

      source.connect(processor);
      processor.connect(audioCtx.destination);

      recordingRef.current = { stream, audioCtx, processor, chunks };
      setMicState('recording');
    } catch (err) {
      console.error('Microphone access failed', err);
      setMicState('idle');
    }
  }, [setMicState, submitRecording]);

  useEffect(() => {
    const el = inputRef.current;
    if (!el) return;

    const handleInputKeyDown = (e: KeyboardEvent) => {
      if (disabledRef.current) return;

      if (showSlashMenuRef.current) {
        // Once the user has typed a space after the command (e.g.
        // `/agent plan`), the slash menu is irrelevant — the input is now
        // an argument to the command, not a command filter. Let Enter fall
        // through to the normal submit path so the args are parsed and
        // dispatched properly. Arrow / Escape / Tab still apply when the
        // caret is somewhere we can usefully navigate the menu.
        const hasArg = el.value.includes(' ');
        const cmds = filteredCommandsRef.current;
        if (e.key === 'ArrowDown' && !hasArg) {
          e.preventDefault();
          const next = (slashIndexRef.current + 1) % Math.max(cmds.length, 1);
          slashIndexRef.current = next;
          el.dispatchEvent(new CustomEvent('oc-slash-nav', { detail: next }));
          return;
        }
        if (e.key === 'ArrowUp' && !hasArg) {
          e.preventDefault();
          const next = (slashIndexRef.current - 1 + Math.max(cmds.length, 1)) % Math.max(cmds.length, 1);
          slashIndexRef.current = next;
          el.dispatchEvent(new CustomEvent('oc-slash-nav', { detail: next }));
          return;
        }
        if (e.key === 'Escape') {
          e.preventDefault();
          el.dispatchEvent(new CustomEvent('oc-slash-close'));
          return;
        }
        if ((e.key === 'Tab' || (e.key === 'Enter' && !e.shiftKey)) && !hasArg) {
          if (cmds.length > 0) {
            e.preventDefault();
            const cmd = cmds[slashIndexRef.current];
            if (cmd) {
              el.dispatchEvent(new CustomEvent('oc-slash-select', { detail: cmd }));
            }
            return;
          }
        }
      }

      if (e.key === 'Tab' && !e.ctrlKey && !e.metaKey && !e.altKey) {
        const opts = agentOptionsRef.current;
        const onChange = onAgentChangeRef.current;
        if (opts.length > 0 && onChange) {
          e.preventDefault();
          const current = effectiveAgentRef.current;
          const idx = opts.indexOf(current);
          const dir = e.shiftKey ? -1 : 1;
          const nextIdx = idx === -1
            ? (e.shiftKey ? opts.length - 1 : 0)
            : (idx + dir + opts.length) % opts.length;
          onChange(opts[nextIdx]);
          return;
        }
      }

      if (e.key === 'c' && e.ctrlKey && el.value.trim() && el.selectionStart === el.selectionEnd) {
        e.preventDefault();
        setIsBashModeRef.current(false);
        el.value = '';
        el.style.height = 'auto';
        el.dispatchEvent(new CustomEvent('oc-slash-update', { detail: { show: false, filter: '' } }));
        el.dispatchEvent(new CustomEvent('oc-clear-images'));
        const sid = sessionIdRef.current;
        if (sid) {
          if (draftTimerRef.current) clearTimeout(draftTimerRef.current);
          clearDraft(sid);
        }
        return;
      }

      if (e.key === 'Enter' && !e.shiftKey) {
        e.preventDefault();
        const trimmed = el.value.trim();
        const imgs = imagesRef.current;
        if (!trimmed && imgs.length === 0) return;

        if (trimmed.startsWith('/') && onCommandRef.current) {
          const spaceIdx = trimmed.indexOf(' ');
          const command = spaceIdx > 0 ? trimmed.slice(1, spaceIdx) : trimmed.slice(1);
          const args = spaceIdx > 0 ? trimmed.slice(spaceIdx + 1).trim() : '';
          // /model and /agent are client-only: don't dispatch to the backend.
          if (command === 'model' || command === 'agent') {
            el.value = '';
            el.style.height = 'auto';
            const sid = sessionIdRef.current;
            if (sid) {
              if (draftTimerRef.current) clearTimeout(draftTimerRef.current);
              clearDraft(sid);
            }
            const evt = command === 'model' ? 'oc-model-picker-open' : 'oc-agent-picker-open';
            el.dispatchEvent(new CustomEvent(evt, { detail: args }));
            return;
          }
          onCommandRef.current(command, args);
        } else {
          onSendRef.current?.(trimmed, imgs.length > 0 ? imgs : undefined);
        }

        setIsBashModeRef.current(false);
        el.value = '';
        el.style.height = 'auto';
        el.dispatchEvent(new CustomEvent('oc-slash-update', { detail: { show: false, filter: '' } }));
        const sid = sessionIdRef.current;
        if (sid) {
          if (draftTimerRef.current) clearTimeout(draftTimerRef.current);
          clearDraft(sid);
        }
        el.dispatchEvent(new CustomEvent('oc-clear-images'));
      }
    };

    const handleInput = () => {
      el.style.height = 'auto';
      el.style.height = Math.min(el.scrollHeight, 200) + 'px';

      const val = el.value;
      // Detect bash mode when input starts with !
      el.dispatchEvent(new CustomEvent('oc-bash-mode', { detail: val.startsWith('!') }));
      
      // Only show the slash menu while the user is typing the command name
      // itself. Once a space appears (or a newline), the caret has moved on
      // to arguments (e.g. `/agent plan`) and the menu becomes noise.
      if (val.startsWith('/') && !val.includes(' ') && !val.includes('\n')) {
        const filter = val.slice(1);
        el.dispatchEvent(new CustomEvent('oc-slash-update', { detail: { show: true, filter } }));
      } else {
        el.dispatchEvent(new CustomEvent('oc-slash-update', { detail: { show: false, filter: '' } }));
      }
      const sid = sessionIdRef.current;
      if (sid) {
        if (draftTimerRef.current) clearTimeout(draftTimerRef.current);
        draftTimerRef.current = setTimeout(() => {
          const text = el.value.trim();
          if (text) {
            saveDraft(sid, text);
          } else {
            clearDraft(sid);
          }
        }, 300);
      }
    };

    const handlePaste = (e: ClipboardEvent) => {
      if (disabledRef.current) return;
      const items = e.clipboardData?.items;
      if (!items) return;
      const imageFiles: File[] = [];
      for (let i = 0; i < items.length; i++) {
        if (items[i].type.startsWith('image/')) {
          const file = items[i].getAsFile();
          if (file) imageFiles.push(file);
        }
      }
      if (imageFiles.length > 0) {
        e.preventDefault();
        el.dispatchEvent(new CustomEvent('oc-paste-images', { detail: imageFiles }));
      }
    };

    el.addEventListener('keydown', handleInputKeyDown);
    el.addEventListener('input', handleInput);
    el.addEventListener('paste', handlePaste);

    return () => {
      if (draftTimerRef.current) {
        clearTimeout(draftTimerRef.current);
        draftTimerRef.current = null;
      }
      el.removeEventListener('keydown', handleInputKeyDown);
      el.removeEventListener('input', handleInput);
      el.removeEventListener('paste', handlePaste);
    };
  }, []);

  useEffect(() => {
    const handleKeyDown = (e: KeyboardEvent) => {
      if (e.defaultPrevented) return;
      if (e.key === 'Escape' && isRunningRef.current && onAbortRef.current) {
        e.preventDefault();
        onAbortRef.current();
      }
    };
    window.addEventListener('keydown', handleKeyDown);
    return () => window.removeEventListener('keydown', handleKeyDown);
  }, []);

  useEffect(() => {
    const el = inputRef.current;
    if (!el) return;
    const handleClearImages = () => setImages([]);
    const handlePasteImages = (e: Event) => {
      const files = (e as CustomEvent).detail as File[];
      addImageFiles(files);
    };
    const handleSlashUpdate = (e: Event) => {
      const { show, filter } = (e as CustomEvent).detail as { show: boolean; filter: string };
      setShowSlashMenu(show);
      setSlashFilter(filter);
      if (!show) setSlashIndex(0);
    };
    const handleSlashNav = (e: Event) => {
      setSlashIndex((e as CustomEvent).detail as number);
    };
    const handleSlashClose = () => {
      setShowSlashMenu(false);
      setSlashFilter('');
      setSlashIndex(0);
    };
    const handleSlashSelect = (e: Event) => {
      selectSlashCommand((e as CustomEvent).detail as SlashCommand);
    };
    const handleModelPickerOpen = (e: Event) => {
      openModelPicker(((e as CustomEvent).detail as string) || '');
    };
    const handleAgentPickerOpen = (e: Event) => {
      openAgentPicker(((e as CustomEvent).detail as string) || '');
    };
    const handleBashMode = (e: Event) => {
      setIsBashMode((e as CustomEvent).detail as boolean);
    };
    el.addEventListener('oc-clear-images', handleClearImages);
    el.addEventListener('oc-paste-images', handlePasteImages);
    el.addEventListener('oc-slash-update', handleSlashUpdate);
    el.addEventListener('oc-slash-nav', handleSlashNav);
    el.addEventListener('oc-slash-close', handleSlashClose);
    el.addEventListener('oc-slash-select', handleSlashSelect);
    el.addEventListener('oc-model-picker-open', handleModelPickerOpen);
    el.addEventListener('oc-agent-picker-open', handleAgentPickerOpen);
    el.addEventListener('oc-bash-mode', handleBashMode);
    return () => {
      el.removeEventListener('oc-clear-images', handleClearImages);
      el.removeEventListener('oc-paste-images', handlePasteImages);
      el.removeEventListener('oc-slash-update', handleSlashUpdate);
      el.removeEventListener('oc-slash-nav', handleSlashNav);
      el.removeEventListener('oc-slash-close', handleSlashClose);
      el.removeEventListener('oc-slash-select', handleSlashSelect);
      el.removeEventListener('oc-model-picker-open', handleModelPickerOpen);
      el.removeEventListener('oc-agent-picker-open', handleAgentPickerOpen);
      el.removeEventListener('oc-bash-mode', handleBashMode);
    };
  }, [addImageFiles, selectSlashCommand, openModelPicker, openAgentPicker]);

  const handleDragOver = useCallback((e: React.DragEvent) => {
    e.preventDefault();
    e.stopPropagation();
  }, []);

  const handleDrop = useCallback((e: React.DragEvent) => {
    e.preventDefault();
    e.stopPropagation();
    if (disabled) return;
    const files = Array.from(e.dataTransfer.files);
    addImageFiles(files);
  }, [disabled, addImageFiles]);

  useEffect(() => {
    if (!showTokenPopover) return;
    const handler = (e: MouseEvent) => {
      if (tokenPopoverRef.current && !tokenPopoverRef.current.contains(e.target as Node)) {
        setShowTokenPopover(false);
      }
    };
    document.addEventListener('mousedown', handler);
    return () => document.removeEventListener('mousedown', handler);
  }, [showTokenPopover]);

  // Fetch estimated cost from backend when the popover opens (or stats change
  // while it's already open). The backend uses the pricing table loaded at
  // startup to compute the cost from the token counts and active model.
  useEffect(() => {
    if (!showTokenPopover || !tokenStats || !activeModel) return;
    let cancelled = false;
    // Schedule the loading flag in a microtask to avoid a synchronous setState
    // inside the effect body (react-hooks/set-state-in-effect).
    Promise.resolve().then(() => { if (!cancelled) setEstCostLoading(true); });
    api.calcCost({
      modelID: activeModel,
      input: tokenStats.input,
      output: tokenStats.output,
      cacheRead: tokenStats.cacheRead,
      cacheWrite: tokenStats.cacheWrite,
    }).then((result) => {
      if (!cancelled) {
        setEstCost(result);
        setEstCostLoading(false);
      }
    }).catch(() => {
      if (!cancelled) {
        setEstCost(null);
        setEstCostLoading(false);
      }
    });
    return () => { cancelled = true; };
  }, [showTokenPopover, activeModel, tokenStats?.input, tokenStats?.output, tokenStats?.cacheRead, tokenStats?.cacheWrite, tokenStats]);

  const agentOptions = Array.from(new Set([activeAgent, ...KNOWN_AGENTS].filter((a): a is string => !!a)));
  const effectiveAgent = selectedAgent || activeAgent || '';
  useEffect(() => { agentOptionsRef.current = agentOptions; }, [agentOptions]);
  useEffect(() => { effectiveAgentRef.current = effectiveAgent; }, [effectiveAgent]);
  const effectiveModel = selectedModel || activeModel || '';
  // Label shown on the composer's model button. Prefer the human-readable
  // `modelName` from the rich entries (e.g. "Claude Opus 4.7"), falling back
  // to the bare model id from the "provider/model" string.
  const modelButtonLabel = (() => {
    if (!effectiveModel) return '';
    const match = modelEntries?.find((e) => e.provider && `${e.provider}/${e.model}` === effectiveModel);
    if (match) return match.modelName || match.model;
    const slash = effectiveModel.indexOf('/');
    return slash > 0 ? effectiveModel.slice(slash + 1) : effectiveModel;
  })();
  // Reasoning options for the effective model, derived from the model entries.
  const reasoningOptions: string[] = (() => {
    if (!effectiveModel || !modelEntries) return [];
    const match = modelEntries.find((e) => e.provider && `${e.provider}/${e.model}` === effectiveModel);
    return match?.reasoning ?? [];
  })();
  const hasReasoning = reasoningOptions.length > 0;
  const onReasoningChangeRef = useRef(onReasoningChange);
  useEffect(() => { onReasoningChangeRef.current = onReasoningChange; }, [onReasoningChange]);
  const reasoningOptionsRef = useRef(reasoningOptions);
  useEffect(() => { reasoningOptionsRef.current = reasoningOptions; }, [reasoningOptions]);

  useEffect(() => {
    if (!isRecording) return;
    const onKeyDown = (e: KeyboardEvent) => {
      if (e.code === 'Escape') {
        e.preventDefault();
        cancelRecording();
      } else {
        e.preventDefault();
        void submitRecording();
      }
    };
    window.addEventListener('keydown', onKeyDown, true);
    return () => window.removeEventListener('keydown', onKeyDown, true);
  }, [isRecording, submitRecording, cancelRecording]);

  const handleMicClickRef = useRef(handleMicClick);
  useEffect(() => { handleMicClickRef.current = handleMicClick; }, [handleMicClick]);

  const dictationShortcut = useMemo(() => ({
    id: 'composer.dictation',
    scope: 'composer' as const,
    keys: { code: 'KeyD', alt: true },
    description: 'Start dictation (voice input)',
    enabled: () => !!whisperAvailable && !isRecording && !disabledRef.current,
    handler: () => { void handleMicClickRef.current(); },
  }), [whisperAvailable, isRecording]);

  const reasoningCycleShortcut = useMemo(() => ({
    id: 'composer.reasoning-cycle',
    scope: 'composer' as const,
    keys: { code: 'KeyR', alt: true },
    description: 'Cycle reasoning level',
    enabled: () => reasoningOptionsRef.current.length > 0 && !disabledRef.current,
    handler: () => {
      const opts = reasoningOptionsRef.current;
      if (opts.length === 0) return;
      const cb = onReasoningChangeRef.current;
      if (!cb) return;
      const cycle = ['', ...opts];
      const cur = cycle.indexOf(selectedReasoning ?? '');
      const next = cycle[(cur + 1) % cycle.length];
      cb(next);
    },
  }), [selectedReasoning]);

  useShortcut(dictationShortcut);
  useShortcut(reasoningCycleShortcut);

  return (
    <>
    {isRecording && createPortal(
      <div className="oc-recording-overlay" onClick={() => void submitRecording()}>
        <div className="oc-recording-inner">
          <div className="oc-recording-pulse" />
          <div className="oc-recording-label">Recording</div>
          <div className="oc-recording-hint">Press any key or click to submit &nbsp;·&nbsp; Esc to cancel</div>
        </div>
      </div>,
      document.body,
    )}
    <div
      className={`oc-composer-wrap${disabled ? ' oc-composer-disabled' : ''}`}
      ref={wrapRef}
      onDragOver={handleDragOver}
      onDrop={handleDrop}
    >
      {modelPickerOpen && (
        <ModelPicker
          open={modelPickerOpen}
          models={models || []}
          modelEntries={modelEntries}
          currentModel={effectiveModel}
          initialQuery={modelPickerQuery}
          onSelect={(m) => onModelChange?.(m)}
          onClose={() => { setModelPickerOpen(false); inputRef.current?.focus(); }}
        />
      )}
      {agentPickerOpen && (
        <AgentPicker
          open={agentPickerOpen}
          agentNames={agentOptions}
          agents={agents}
          activeAgent={activeAgent}
          currentAgent={effectiveAgent}
          initialQuery={agentPickerQuery}
          onSelect={(a) => onAgentChange?.(a)}
          onClose={() => { setAgentPickerOpen(false); inputRef.current?.focus(); }}
        />
      )}
      {reasoningPickerOpen && (
        <ReasoningPicker
          open={reasoningPickerOpen}
          options={reasoningOptions}
          current={selectedReasoning}
          onSelect={(v) => onReasoningChange?.(v)}
          onClose={() => { setReasoningPickerOpen(false); inputRef.current?.focus(); }}
        />
      )}
      {showSlashMenu && filteredCommands.length > 0 && (
        <div className="oc-slash-menu" ref={slashMenuRef}>
          {filteredCommands.map((cmd, i) => (
            <div
              key={cmd.name}
              className={`oc-slash-item${i === slashIndex ? ' active' : ''}`}
              onMouseDown={(e) => { e.preventDefault(); selectSlashCommand(cmd); }}
              onMouseEnter={() => setSlashIndex(i)}
            >
              <span className="oc-slash-name">/{cmd.name}</span>
              {cmd.description && <span className="oc-slash-desc">{cmd.description}</span>}
            </div>
          ))}
        </div>
      )}
      <div
        className="oc-composer"
        // Only colorize once the /agent catalog has resolved — otherwise the
        // fallback color (e.g. `build` → mauve) paints briefly before the
        // authoritative color arrives, producing a pink flash.
        // Red border when in bash mode (input starts with !)
        style={
          isBashMode
            ? { borderLeftColor: '#f38ba8' }
            : !disabled && effectiveAgent && agentsLoaded
            ? { borderLeftColor: agentColor(effectiveAgent, agents) }
            : undefined
        }
      >
        {images.length > 0 && (
          <div className="oc-composer-images">
            {images.map((img, i) => (
              <div key={i} className="oc-composer-image-thumb">
                <img src={img.url} alt={`Attachment ${i + 1}`} />
                <button className="oc-composer-image-remove" onClick={() => removeImage(i)}>{'\u00D7'}</button>
              </div>
            ))}
          </div>
        )}
        <textarea
          ref={inputRef}
          className="oc-composer-input"
          rows={1}
          disabled={disabled}
          placeholder={disabled ? (disabledHint || 'No live connection to the agent') : undefined}
        />
        <div className="oc-composer-bar">
          <div className="oc-composer-bar-left">
            {isBashMode ? (
              <span className="oc-bar-shell">shell</span>
            ) : (
              <>
                <button
                  type="button"
                  className="oc-bar-select"
                  disabled={disabled}
                  onClick={() => {
                    if (disabled) return;
                    setAgentPickerQuery('');
                    setAgentPickerOpen(true);
                  }}
                  title="Agent (click to change)"
                >
                  {effectiveAgent && agentsLoaded && (
                    <span
                      className="oc-agent-swatch"
                      aria-hidden="true"
                      style={{ background: agentColor(effectiveAgent, agents) }}
                    />
                  )}
                  {effectiveAgent || 'Agent'}
                </button>
                {hasModels && (
                  <button
                    type="button"
                    className="oc-bar-select"
                    disabled={disabled}
                    onClick={() => {
                      if (disabled) return;
                      setModelPickerQuery('');
                      setModelPickerOpen(true);
                    }}
                    title="Model (click to change)"
                  >
                    {modelButtonLabel || 'Model'}
                  </button>
                )}
                {hasReasoning && (
                  <button
                    type="button"
                    className="oc-bar-select oc-bar-reasoning"
                    disabled={disabled}
                    onClick={() => {
                      if (disabled) return;
                      setReasoningPickerOpen(true);
                    }}
                    title={`Reasoning level (${isMacPlatform() ? '⌥' : 'Alt'}+R to cycle)`}
                  >
                    {selectedReasoning || 'default'}
                  </button>
                )}
                {disabled && (
                  <span className="oc-bar-hint" title={disabledHint || undefined}>
                    No live connection
                  </span>
                )}
              </>
            )}
          </div>
          <div className="oc-composer-bar-right">
            <button
              className="oc-bar-action"
              onClick={() => fileInputRef.current?.click()}
              disabled={disabled}
              title="Attach image"
            >+</button>
            <input
              ref={fileInputRef}
              type="file"
              accept="image/*"
              multiple
              style={{ display: 'none' }}
              onChange={(e) => {
                const files = Array.from(e.target.files || []);
                addImageFiles(files);
                e.target.value = '';
              }}
            />
            {whisperAvailable && (
              <button
                ref={micRef}
                className="oc-bar-action"
                onClick={handleMicClick}
                disabled={disabled}
                title="Record voice message"
              >
                <i className="bi bi-mic-fill oc-mic-icon" aria-hidden="true" />
              </button>
            )}
          </div>
        </div>
      </div>
      <div className="oc-composer-footer">
        <span className="oc-composer-footer-left">
          {!disabled && isRunning && (
            <>
              <span
                className="oc-bar-dots"
                // Tint the progressing dots with the active agent's colour so
                // the user can see at a glance which agent is generating.
                // Same gating as the composer's left border: only apply once
                // the /agent catalog has resolved, otherwise fall through to
                // the CSS default (`--accent4`).
                style={effectiveAgent && agentsLoaded
                  // Cast because TS doesn't know about custom CSS properties
                  // on inline styles. The shape is still a valid style object.
                  ? { '--oc-dot-color': agentColor(effectiveAgent, agents) } as Record<string, string>
                  : undefined}
              >
                <span className="oc-thinking-dot" /><span className="oc-thinking-dot" /><span className="oc-thinking-dot" />
              </span>
              {tokensPerSecond != null && tokensPerSecond > 0 && (
                <span className="oc-tps-hint">{tokensPerSecond.toFixed(1)} tok/s</span>
              )}
              <button
                type="button"
                className="oc-stop-btn"
                onClick={() => onAbort?.()}
                title="Stop generation (Esc)"
              >
                <svg width="10" height="10" viewBox="0 0 10 10" aria-hidden="true">
                  <rect x="1" y="1" width="8" height="8" rx="1.5" fill="currentColor" />
                </svg>
              </button>
              <span className="oc-stop-hint">Esc to interrupt</span>
            </>
          )}
        </span>
        <span className="oc-composer-footer-right">
          {contextTokens != null && contextTokens > 0 && (() => {
            const contextWindow = getContextWindow(activeModel);
            const pct = contextWindow ? Math.min(100, (contextTokens / contextWindow) * 100) : null;
            return (
              <span className="oc-context-usage-wrap" ref={tokenPopoverRef}>
                <button
                  type="button"
                  className={`oc-context-usage${pct != null && pct > 80 ? ' oc-context-warn' : ''}`}
                  title="Click for token usage details"
                  onClick={() => setShowTokenPopover(v => !v)}
                >
                  {formatTokenCount(contextTokens)}{pct != null && ` (${pct.toFixed(0)}%)`}
                </button>
                {showTokenPopover && tokenStats && (
                  <div className="oc-token-popover">
                    <div className="oc-token-popover-title">Session token usage</div>
                    <div className="oc-token-popover-rows">
                      <div className="oc-token-popover-row">
                        <span className="oc-token-popover-label">Input</span>
                        <span className="oc-token-popover-value">{tokenStats.input.toLocaleString()}</span>
                      </div>
                      <div className="oc-token-popover-row">
                        <span className="oc-token-popover-label">Output</span>
                        <span className="oc-token-popover-value">{tokenStats.output.toLocaleString()}</span>
                      </div>
                      {tokenStats.reasoning > 0 && (
                        <div className="oc-token-popover-row">
                          <span className="oc-token-popover-label">Reasoning</span>
                          <span className="oc-token-popover-value">{tokenStats.reasoning.toLocaleString()}</span>
                        </div>
                      )}
                      {(tokenStats.cacheRead > 0 || tokenStats.cacheWrite > 0) && (
                        <div className="oc-token-popover-row">
                          <span className="oc-token-popover-label">Cache read</span>
                          <span className="oc-token-popover-value">{tokenStats.cacheRead.toLocaleString()}</span>
                        </div>
                      )}
                      {tokenStats.cacheWrite > 0 && (
                        <div className="oc-token-popover-row">
                          <span className="oc-token-popover-label">Cache write</span>
                          <span className="oc-token-popover-value">{tokenStats.cacheWrite.toLocaleString()}</span>
                        </div>
                      )}
                      <div className="oc-token-popover-divider" />
                      <div className="oc-token-popover-row">
                        <span className="oc-token-popover-label">Context used</span>
                        <span className="oc-token-popover-value">
                          {contextTokens.toLocaleString()}
                          {contextWindow ? ` / ${contextWindow.toLocaleString()}` : ''}
                          {pct != null ? ` (${pct.toFixed(0)}%)` : ''}
                        </span>
                      </div>
                      {tokenStats.totalCost > 0 && (
                        <div className="oc-token-popover-row">
                          <span className="oc-token-popover-label">Reported cost</span>
                          <span className="oc-token-popover-value">${tokenStats.totalCost.toFixed(4)}</span>
                        </div>
                      )}
                      <div className="oc-token-popover-divider" />
                      <div className="oc-token-popover-row oc-token-popover-cost">
                        <span className="oc-token-popover-label">Est. cost</span>
                        <span className="oc-token-popover-value">
                          {estCostLoading ? '…' : estCost ? (estCost.known ? `$${estCost.cost.toFixed(4)}` : 'unknown model') : 'n/a'}
                        </span>
                      </div>
                    </div>
                  </div>
                )}
              </span>
            );
          })()}
          <button type="button" className="oc-keybind-hint" onClick={openShortcuts}>{isMacPlatform() ? '⌥+?' : 'Alt+?'} for shortcuts</button>
        </span>
      </div>
    </div>
    </>
  );
}

export const Composer = memo(ComposerImpl, (prev, next) =>
  prev.isRunning === next.isRunning &&
  prev.disabled === next.disabled &&
  prev.disabledHint === next.disabledHint &&
  prev.whisperAvailable === next.whisperAvailable &&
  prev.activeModel === next.activeModel &&
  prev.selectedModel === next.selectedModel &&
  prev.activeAgent === next.activeAgent &&
  prev.selectedAgent === next.selectedAgent &&
  prev.agents === next.agents &&
  prev.agentsLoaded === next.agentsLoaded &&
  prev.contextTokens === next.contextTokens &&
  prev.sessionId === next.sessionId &&
  prev.tokensPerSecond === next.tokensPerSecond &&
  prev.tokenStats?.input === next.tokenStats?.input &&
  prev.tokenStats?.output === next.tokenStats?.output &&
  prev.tokenStats?.totalCost === next.tokenStats?.totalCost &&
  prev.selectedReasoning === next.selectedReasoning &&
  (prev.models?.length || 0) === (next.models?.length || 0) &&
  (prev.models || []).every((model, i) => model === (next.models || [])[i])
);
