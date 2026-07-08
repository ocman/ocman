import { useState, useEffect, useRef, useCallback, useMemo, memo, type ReactNode } from 'react';
import './Composer.css';
import { getDraft, saveDraft, clearDraft } from '../../lib/composerDraft';
import { isMacPlatform } from '../../lib/shortcuts';
import { useShortcut } from '../../lib/shortcutRegistry';
import { useUiStore } from '../../lib/uiStore';
import { api, type SlashCommand, type AgentInfo, type SessionModelEntry } from '../../lib/api';
import { agentColor } from '../../lib/agentColor';
import { ModelPicker } from './ModelPicker';
import { AgentPicker } from './AgentPicker';
import { ReasoningPicker } from './ReasoningPicker';
import { BranchSelector, TargetSelector } from './ComposerSelectorRow';
import { SlashCommandMenu } from './SlashCommandMenu';
import { useComposerAudio } from './useComposerAudio';
import { routeComposerSubmit } from './composerSubmit';
import { getContextWindow, formatTokenCount } from '../../lib/models/contextWindows';
import { formatCurrency, formatTokensPerSecond } from '../../lib/format';
import { BUILTIN_COMMANDS, KNOWN_AGENTS } from '../../lib/commands/builtinCommands';
import { remoteLog } from '../../lib/remoteLog';

export interface AttachedImage {
  url: string;
  mime: string;
}

interface AttachedFileRef {
  path: string;
  name: string;
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

function ComposerImpl({
  onSend,
  onCommand,
  onShell,
  shellExec,
  queuedShellCommand,
  onCancelQueuedShell,
  onAbort,
  isRunning,
  disabled,
  whisperAvailable,
  models,
  modelEntries,
  selectedModel,
  onModelChange,
  onToggleFavorite,
  onRefreshModels,
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
  onLaunchRequest,
  launching,
  directory,
  newConversation,
  worktreesSupported,
  permissionControl,
}: {
  onSend?: (text: string, images?: AttachedImage[]) => void;
  onCommand?: (command: string, args: string) => void;
  /**
   * Called when the user submits a `!`-prefixed shell command on a
   * platform that reports caps.shellExec. Receives the command with
   * the `!` already stripped. Wired in the parent to api.runShell.
   */
  onShell?: (command: string) => void;
  /**
   * Capability flag from the active platform (caps.shellExec).
   * When false, `!`-prefixed input falls through to onSend as a
   * normal LLM prompt — preserving today's behaviour on platforms
   * without a shell-tool primitive (Claude Code).
   */
  shellExec?: boolean;
  /**
   * A `!`-prefixed shell command that was submitted while the agent
   * was streaming and is now waiting for the turn to finish before it
   * runs. Surfaced in the footer so the user knows the command was
   * accepted and what we're waiting for. Null when nothing is queued.
   */
  queuedShellCommand?: string | null;
  /** Drop the queued shell command without running it. */
  onCancelQueuedShell?: () => void;
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
  selectedModel?: string;
  onModelChange?: (model: string) => void;
  /**
   * Toggle favorite on a model. Called with (provider, model, nextFavorite)
   * where nextFavorite is the state to move to (true = favorite, false =
   * unfavorite). The parent is expected to call the /api/favorites endpoint
   * and refresh modelEntries; the picker stays dumb.
   */
  onToggleFavorite?: (provider: string, model: string, nextFavorite: boolean) => void;
  /**
   * Fire-and-forget callback invoked every time the user opens the model
   * picker (slash command, palette, model badge button, or `/model` event).
   * Lets the parent re-fetch the session-scoped model catalog so newly
   * configured providers / models surface without a page reload. The picker
   * opens with whatever data is currently in `modelEntries`; the refresh
   * flows in via the next `modelEntries` prop update.
   */
  onRefreshModels?: () => void;
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
  /**
   * Called when the user clicks the disabled composer area or its "Launch"
   * button to start the agent process. Only wired when the composer is
   * disabled due to a missing live connection (not while a pending prompt is
   * active).
   */
  onLaunchRequest?: () => void;
  /** Whether a launch triggered via onLaunchRequest is in flight. */
  launching?: boolean;
  /**
   * Absolute session/project directory, used by the selector row below
   * the composer (branch switcher + worktree target). Omitted → the row
   * is hidden.
   */
  directory?: string;
  /**
   * True when this composer belongs to a conversation with no messages
   * yet. Enables the left "Current checkout / New worktree" target
   * select; disabled otherwise (the target is fixed once a session has
   * started).
   */
  newConversation?: boolean;
  /** Host capability: worktree creation available here (gates the option). */
  worktreesSupported?: boolean;
  permissionControl?: ReactNode;
}) {
  const [showTokenPopover, setShowTokenPopover] = useState(false);
  const [estCost, setEstCost] = useState<{ cost: number; known: boolean } | null>(null);
  const [estCostLoading, setEstCostLoading] = useState(false);
  const tokenPopoverRef = useRef<HTMLDivElement>(null);
  const openShortcuts = useUiStore((s) => s.openShortcuts);

  const wrapRef = useRef<HTMLDivElement>(null);
  const inputRef = useRef<HTMLTextAreaElement>(null);
  const fileInputRef = useRef<HTMLInputElement>(null);
  const onSendRef = useRef(onSend);
  const isRunningRef = useRef(isRunning);
  const disabledRef = useRef(disabled);
  const [images, setImages] = useState<AttachedImage[]>([]);
  const [files, setFiles] = useState<AttachedFileRef[]>([]);
  const imagesRef = useRef<AttachedImage[]>([]);
  const filesRef = useRef<AttachedFileRef[]>([]);
  const sessionIdRef = useRef(sessionId);
  const draftTimerRef = useRef<ReturnType<typeof setTimeout> | null>(null);
  const onAbortRef = useRef(onAbort);

  useEffect(() => { imagesRef.current = images; }, [images]);
  useEffect(() => { filesRef.current = files; }, [files]);
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
  const onShellRef = useRef(onShell);
  useEffect(() => { onShellRef.current = onShell; }, [onShell]);
  const shellExecRef = useRef(!!shellExec);
  useEffect(() => { shellExecRef.current = !!shellExec; }, [shellExec]);
  const agentOptionsRef = useRef<string[]>([]);
  const effectiveAgentRef = useRef<string>('');
  const onAgentChangeRef = useRef(onAgentChange);
  useEffect(() => { onAgentChangeRef.current = onAgentChange; }, [onAgentChange]);
  const [slashCommands, setSlashCommands] = useState<SlashCommand[]>(BUILTIN_COMMANDS);
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
  const onRefreshModelsRef = useRef(onRefreshModels);
  useEffect(() => { modelsRef.current = models; }, [models]);
  useEffect(() => { onModelChangeRef.current = onModelChange; }, [onModelChange]);
  useEffect(() => { agentsRef.current = agents; }, [agents]);
  useEffect(() => { onRefreshModelsRef.current = onRefreshModels; }, [onRefreshModels]);
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
    // Fire-and-forget: pull the latest provider catalog. The picker opens
    // with current data; the refresh flows in via a `modelEntries` prop
    // update on the next render.
    onRefreshModelsRef.current?.();
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
    const newImages: AttachedImage[] = [];
    for (const file of imageFiles) {
      try {
        const url = await readFileAsDataURL(file);
        newImages.push({ url, mime: file.type });
      } catch (err) {
        remoteLog.error('Failed to read image', err);
      }
    }
    if (newImages.length > 0) setImages(prev => [...prev, ...newImages]);

    const otherFiles = files.filter(f => !f.type.startsWith('image/'));
    if (otherFiles.length === 0) return;
    const sid = sessionIdRef.current;
    if (!sid) return;
    const newFiles: AttachedFileRef[] = [];
    for (const file of otherFiles) {
      try {
        const saved = await api.uploadComposerAttachment(sid, file);
        newFiles.push({
          path: saved.path,
          name: saved.name || file.name,
          mime: saved.mime || file.type || 'application/octet-stream',
        });
      } catch (err) {
        remoteLog.error('Failed to save attachment', err);
      }
    }
    if (newFiles.length > 0) setFiles(prev => [...prev, ...newFiles]);
  }, []);

  const removeImage = useCallback((index: number) => {
    setImages(prev => prev.filter((_, i) => i !== index));
  }, []);

  const removeFile = useCallback((index: number) => {
    setFiles(prev => prev.filter((_, i) => i !== index));
  }, []);

  useEffect(() => {
    const el = inputRef.current;
    if (!el) return;
    el.disabled = !!disabled;
  }, [disabled]);

  // ---------------------------------------------------------------------------
  // Audio recording — delegated to useComposerAudio hook
  // ---------------------------------------------------------------------------

  const {
    isRecording,
    micError,
    setMicError,
    micRef,
    handleMicClick,
    isDictationSupported,
  } = useComposerAudio({ whisperAvailable, disabled, inputRef });

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
        el.dispatchEvent(new CustomEvent('oc-clear-files'));
        const sid = sessionIdRef.current;
        if (sid) {
          if (draftTimerRef.current) clearTimeout(draftTimerRef.current);
          clearDraft(sid);
        }
        return;
      }

      if (e.key === 'Enter' && !e.shiftKey) {
        e.preventDefault();
        const raw = el.value;
        const imgs = imagesRef.current;
        const fileRefs = filesRef.current;
        const route = routeComposerSubmit(raw, { shellExec: shellExecRef.current });

        // No text: only proceed if there are attachments to reference.
        if (route.kind === 'noop' && imgs.length === 0 && fileRefs.length === 0) return;

        const fileReferenceText = fileRefs.length > 0
          ? `Attached files saved on disk:\n${fileRefs.map(f => `- ${f.path} (${f.mime})`).join('\n')}`
          : '';
        const withFileReferences = (text: string) => [text, fileReferenceText].filter(Boolean).join('\n\n');

        if (route.kind === 'command' && onCommandRef.current) {
          // /model and /agent are client-only: don't dispatch to the backend.
          if (route.command === 'model' || route.command === 'agent') {
            el.value = '';
            el.style.height = 'auto';
            const sid = sessionIdRef.current;
            if (sid && draftTimerRef.current) clearTimeout(draftTimerRef.current);
            if (sid) clearDraft(sid);
            const evt = route.command === 'model' ? 'oc-model-picker-open' : 'oc-agent-picker-open';
            el.dispatchEvent(new CustomEvent(evt, { detail: route.args }));
            return;
          }
          onCommandRef.current(route.command, route.args);
        } else if (route.kind === 'shell' && onShellRef.current) {
          onShellRef.current(route.command);
        } else if (route.kind === 'send' || route.kind === 'noop') {
          // `noop` only reaches here when attachments are present; send
          // with empty text in that case.
          const text = route.kind === 'send' ? route.text : '';
          onSendRef.current?.(withFileReferences(text), imgs.length > 0 ? imgs : undefined);
        } else {
          // route.kind === 'shell' but no onShell handler (capability
          // mis-wiring). Fall back to a plain prompt rather than
          // silently dropping the user's input.
          onSendRef.current?.(withFileReferences(raw.trim()), imgs.length > 0 ? imgs : undefined);
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
        el.dispatchEvent(new CustomEvent('oc-clear-files'));
      }
    };

    const handleInput = () => {
      el.style.height = 'auto';
      el.style.height = Math.min(el.scrollHeight, 200) + 'px';

      const val = el.value;
      // Detect bash mode when input starts with !, but only when the
      // active platform actually supports shell execution. On
      // platforms without it (Claude Code) `!` is just a literal
      // character in the prompt and shouldn't get a special pill.
      el.dispatchEvent(new CustomEvent('oc-bash-mode', { detail: val.startsWith('!') && shellExecRef.current }));
      
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
    const handleClearFiles = () => setFiles([]);
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
      el.value = '';
      el.style.height = 'auto';
      openModelPicker(((e as CustomEvent).detail as string) || '');
    };
    const handleAgentPickerOpen = (e: Event) => {
      el.value = '';
      el.style.height = 'auto';
      openAgentPicker(((e as CustomEvent).detail as string) || '');
    };
    const handleBashMode = (e: Event) => {
      setIsBashMode((e as CustomEvent).detail as boolean);
    };
    el.addEventListener('oc-clear-images', handleClearImages);
    el.addEventListener('oc-clear-files', handleClearFiles);
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
      el.removeEventListener('oc-clear-files', handleClearFiles);
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

  const effectiveModel = selectedModel || '';

  // Fetch estimated cost from backend when the popover opens (or stats change
  // while it's already open). The backend uses the pricing table loaded at
  // startup to compute the cost from the token counts and selected model.
  useEffect(() => {
    if (!showTokenPopover || !tokenStats || !effectiveModel) return;
    let cancelled = false;
    // Schedule the loading flag in a microtask to avoid a synchronous setState
    // inside the effect body (react-hooks/set-state-in-effect).
    Promise.resolve().then(() => { if (!cancelled) setEstCostLoading(true); });
    api.calcCost({
      modelID: effectiveModel,
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
  }, [showTokenPopover, effectiveModel, tokenStats?.input, tokenStats?.output, tokenStats?.cacheRead, tokenStats?.cacheWrite, tokenStats]);

  const agentOptions = Array.from(new Set([activeAgent, ...KNOWN_AGENTS].filter((a): a is string => !!a)));
  const effectiveAgent = selectedAgent || activeAgent || '';
  useEffect(() => { agentOptionsRef.current = agentOptions; }, [agentOptions]);
  useEffect(() => { effectiveAgentRef.current = effectiveAgent; }, [effectiveAgent]);
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

  const handleMicClickRef = useRef(handleMicClick);
  useEffect(() => { handleMicClickRef.current = handleMicClick; }, [handleMicClick]);

  const dictationShortcut = useMemo(() => ({
    id: 'composer.dictation',
    scope: 'composer' as const,
    keys: { code: 'KeyD', alt: true },
    description: 'Start dictation (voice input)',
    enabled: () => !!(isDictationSupported && !isRecording && !disabledRef.current),
    handler: () => { void handleMicClickRef.current(); },
  }), [isDictationSupported, isRecording]);

  const reasoningCycleShortcut = useMemo(() => ({
    id: 'composer.reasoning-cycle',
    scope: 'composer' as const,
    keys: { code: 'KeyR', alt: true },
    description: 'Cycle reasoning level',
    enabled: () => !!(reasoningOptionsRef.current.length > 0 && !disabledRef.current),
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
    <div
      className={`oc-composer-wrap${disabled ? ' oc-composer-disabled' : ''}`}
      ref={wrapRef}
      onDragOver={handleDragOver}
      onDrop={handleDrop}
      onClick={disabled && onLaunchRequest ? onLaunchRequest : undefined}
      style={disabled && onLaunchRequest ? { cursor: 'pointer' } : undefined}
    >
      {modelPickerOpen && (
        <ModelPicker
          open={modelPickerOpen}
          models={models || []}
          modelEntries={modelEntries}
          currentModel={effectiveModel}
          initialQuery={modelPickerQuery}
          onSelect={(m) => onModelChange?.(m)}
          onToggleFavorite={onToggleFavorite}
          onClose={() => { setModelPickerOpen(false); inputRef.current?.focus(); }}
          onBack={() => { setModelPickerOpen(false); useUiStore.getState().openPalette('command'); }}
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
      {showSlashMenu && (
        <SlashCommandMenu
          commands={filteredCommands}
          activeIndex={slashIndex}
          menuRef={slashMenuRef}
          onSelect={selectSlashCommand}
          onHover={setSlashIndex}
        />
      )}
      {isRecording && (
        <div className="oc-recording-banner">
          <div className="oc-recording-pulse" />
          <span className="oc-recording-label">Listening</span>
          <span className="oc-recording-hint">Esc to cancel</span>
          <button
            type="button"
            className="oc-recording-stop"
            onClick={() => void handleMicClick()}
            aria-label="Stop recording"
          >Stop</button>
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
        {(images.length > 0 || files.length > 0) && (
          <div className="oc-composer-images">
            {images.map((img, i) => (
              <div key={i} className="oc-composer-image-thumb">
                <img src={img.url} alt={`Attachment ${i + 1}`} />
                <button className="oc-composer-image-remove" onClick={() => removeImage(i)}>{'\u00D7'}</button>
              </div>
            ))}
            {files.map((file, i) => (
              <div key={file.path} className="oc-composer-file-thumb" title={file.path}>
                <span className="oc-composer-file-icon">file</span>
                <span className="oc-composer-file-name">{file.name}</span>
                <button className="oc-composer-image-remove" onClick={() => removeFile(i)}>{'\u00D7'}</button>
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
          autoComplete="off"
          autoCorrect="off"
          autoCapitalize="off"
          spellCheck={false}
          data-1p-ignore
          data-lpignore="true"
          data-bwignore
          data-form-type="other"
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
                      onRefreshModelsRef.current?.();
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
                {permissionControl}
                {disabled && onLaunchRequest ? (
                  <button
                    type="button"
                    className="oc-bar-launch"
                    onClick={(e) => { e.stopPropagation(); onLaunchRequest(); }}
                    disabled={launching}
                    title={disabledHint || 'Launch the agent process'}
                  >
                    {launching ? 'Launching…' : 'Launch session'}
                  </button>
                ) : disabled ? (
                  <span className="oc-bar-hint" title={disabledHint || undefined}>
                    No live connection
                  </span>
                ) : null}
              </>
            )}
          </div>
          <div className="oc-composer-bar-right">
            <button
              className="oc-bar-action"
              onClick={() => fileInputRef.current?.click()}
              disabled={disabled}
              title="Attach file"
            >+</button>
            <input
              ref={fileInputRef}
              type="file"
              multiple
              style={{ display: 'none' }}
              onChange={(e) => {
                const files = Array.from(e.target.files || []);
                addImageFiles(files);
                e.target.value = '';
              }}
            />
            {isDictationSupported && (
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
            {micError && (
              <span className="oc-mic-error" role="alert">
                {micError}
                <button
                  type="button"
                  className="oc-mic-error-dismiss"
                  onClick={() => setMicError(null)}
                  aria-label="Dismiss"
                >×</button>
              </span>
            )}
            {isRunning ? (
              <button
                type="button"
                className="oc-bar-send oc-bar-send-stop"
                onClick={() => onAbort?.()}
                title="Stop generation (Esc)"
                aria-label="Stop generation"
              >
                <svg width="12" height="12" viewBox="0 0 10 10" aria-hidden="true">
                  <rect x="1" y="1" width="8" height="8" rx="1.5" fill="currentColor" />
                </svg>
              </button>
            ) : (
              <button
                type="button"
                className="oc-bar-send"
                disabled={disabled}
                title="Send (Enter)"
                aria-label="Send message"
                onClick={() => {
                  // Reuse the textarea's existing Enter submit path so
                  // button and keyboard stay behaviourally identical.
                  inputRef.current?.dispatchEvent(
                    new KeyboardEvent('keydown', { key: 'Enter', bubbles: true }),
                  );
                }}
              >
                <svg width="16" height="16" viewBox="0 0 16 16" aria-hidden="true">
                  <path d="M8 13V3M8 3L4 7M8 3l4 4" stroke="currentColor" strokeWidth="1.6" fill="none" strokeLinecap="round" strokeLinejoin="round" />
                </svg>
              </button>
            )}
          </div>
        </div>
      </div>
      <div className="oc-composer-footer">
        <span className="oc-composer-footer-left">
          {directory && newConversation && (
            <TargetSelector directory={directory} worktreesSupported={!!worktreesSupported} />
          )}
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
                <span className="oc-thinking-dot" /><span className="oc-thinking-dot" /><span className="oc-thinking-dot" /><span className="oc-thinking-dot" /><span className="oc-thinking-dot" />
              </span>
              {tokensPerSecond != null && tokensPerSecond > 0 && (
                <span className="oc-tps-hint">{formatTokensPerSecond(tokensPerSecond)} tok/s</span>
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
          {!disabled && queuedShellCommand && (
            <span className="oc-shell-queued" data-testid="shell-queued">
              <i className="bi bi-hourglass-split oc-shell-queued-icon" aria-hidden="true" />
              <span className="oc-shell-queued-label">
                Shell queued — waiting for the agent to finish:
              </span>
              <code className="oc-shell-queued-cmd" title={queuedShellCommand}>
                {queuedShellCommand}
              </code>
              <button
                type="button"
                className="oc-shell-queued-cancel"
                onClick={() => onCancelQueuedShell?.()}
                title="Cancel queued shell command"
              >Cancel</button>
            </span>
          )}
        </span>
        <span className="oc-composer-footer-right">
          {directory && <BranchSelector directory={directory} />}
          {tokenStats && tokenStats.totalCost > 0 && (
            <span
              className="oc-session-cost"
              title="Session cost reported by the platform"
            >
              {formatCurrency(tokenStats.totalCost)}
            </span>
          )}
          {contextTokens != null && contextTokens > 0 && (() => {
            const contextWindow = getContextWindow(effectiveModel);
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
  prev.queuedShellCommand === next.queuedShellCommand &&
  prev.onCancelQueuedShell === next.onCancelQueuedShell &&
  prev.disabled === next.disabled &&
  prev.disabledHint === next.disabledHint &&
  prev.onLaunchRequest === next.onLaunchRequest &&
  prev.launching === next.launching &&
  prev.whisperAvailable === next.whisperAvailable &&
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
  prev.directory === next.directory &&
  prev.newConversation === next.newConversation &&
  prev.worktreesSupported === next.worktreesSupported &&
  prev.permissionControl === next.permissionControl &&
  (prev.models?.length || 0) === (next.models?.length || 0) &&
  (prev.models || []).every((model, i) => model === (next.models || [])[i])
);
