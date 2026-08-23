import { useState, useEffect, useRef, useCallback, useMemo, useImperativeHandle, type ReactNode, type Ref } from 'react';
import './Composer.css';
import { useComposerDrafts } from './useComposerDrafts';
import { isMacPlatform } from '../../lib/shortcuts';
import { useShortcut } from '../../lib/shortcutRegistry';
import { useUiStore } from '../../lib/uiStore';
import { api, BackendUnavailableError, type SlashCommand, type AgentInfo, type SessionModelEntry } from '../../lib/api';
import { agentColor } from '../../lib/agentColor';
import { ModelPicker } from './ModelPicker';
import { AgentPicker } from './AgentPicker';
import { SkillPicker } from './SkillPicker';
import { ReasoningPicker } from './ReasoningPicker';
import { HelpDialog } from './HelpDialog';
import { useClickOutside } from '../../lib/useClickOutside';
import { TargetSelector } from './ComposerSelectorRow';
import { SlashCommandMenu } from './SlashCommandMenu';
import { QueuedMessages } from './QueuedMessages';
import { useComposerAudio } from './useComposerAudio';
import { routeComposerSubmit } from './composerSubmit';
import { getContextWindow, formatTokenCount } from '../../lib/models/contextWindows';
import { formatCurrency, formatDuration, formatTokensPerSecond } from '../../lib/format';
import { BUILTIN_COMMANDS, KNOWN_AGENTS, modelHasVariants } from '../../lib/commands/builtinCommands';
import { remoteLog } from '../../lib/remoteLog';
import { ModelLabel } from '../ModelLogo';

export interface AttachedImage {
  url: string;
  mime: string;
}

export interface ComposerHandle {
  openModelPicker: (query?: string) => void;
  openAgentPicker: (query?: string) => void;
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

interface ComposerFooterProps {
  directory?: string;
  newConversation?: boolean;
  worktreesSupported?: boolean;
  sessionId?: string;
  disabled?: boolean;
  isRunning: boolean;
  effectiveAgent: string;
  agentsLoaded?: boolean;
  agents?: AgentInfo[];
  tokensPerSecond?: number;
  onAbort?: () => void;
  tokenStats?: {
    input: number;
    output: number;
    reasoning: number;
    cacheRead: number;
    cacheWrite: number;
    totalCost: number;
  };
  sessionTreeStats?: { input: number; output: number; totalCost: number; totalEstCost: number; totalEffectiveCost: number; sessions: number };
  contextTokens?: number;
  effectiveModel: string;
  visibleDurationMs: number;
}

function ComposerFooter({
  directory,
  newConversation,
  worktreesSupported,
  sessionId,
  disabled,
  isRunning,
  effectiveAgent,
  agentsLoaded,
  agents,
  tokensPerSecond,
  onAbort,
  tokenStats,
  sessionTreeStats,
  contextTokens,
  effectiveModel,
  visibleDurationMs,
}: ComposerFooterProps) {
  const [showTokenPopover, setShowTokenPopover] = useState(false);
  const [estCost, setEstCost] = useState<{ cost: number; known: boolean } | null>(null);
  const [estCostLoading, setEstCostLoading] = useState(false);
  const tokenPopoverRef = useRef<HTMLDivElement>(null);
  const openShortcuts = useUiStore((s) => s.openShortcuts);
  const contextWindow = contextTokens ? getContextWindow(effectiveModel) : null;
  const contextPercent = contextTokens && contextWindow
    ? Math.min(100, (contextTokens / contextWindow) * 100)
    : null;

  useClickOutside(tokenPopoverRef, showTokenPopover, () => setShowTokenPopover(false));

  useEffect(() => {
    if (!showTokenPopover || !tokenStats || !effectiveModel) return;
    let cancelled = false;
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
  }, [showTokenPopover, effectiveModel, tokenStats]);

  const effectiveCost = sessionTreeStats?.totalEffectiveCost ?? tokenStats?.totalCost ?? 0;

  return (
    <div className="oc-composer-footer">
      <span className="oc-composer-footer-left">
        {directory && newConversation && (
          <TargetSelector directory={directory} worktreesSupported={!!worktreesSupported} parentSessionId={sessionId} />
        )}
        {!disabled && isRunning && (
          <>
            <span
              className="oc-bar-dots"
              style={effectiveAgent && agentsLoaded
                ? { '--oc-dot-color': agentColor(effectiveAgent, agents) } as Record<string, string>
                : undefined}
            >
              <span className="oc-thinking-dot" /><span className="oc-thinking-dot" /><span className="oc-thinking-dot" /><span className="oc-thinking-dot" /><span className="oc-thinking-dot" />
            </span>
            {tokensPerSecond != null && tokensPerSecond > 0 && (
              <span className="oc-tps-hint">{formatTokensPerSecond(tokensPerSecond)} tok/s</span>
            )}
            <button type="button" className="oc-stop-btn" onClick={onAbort} title="Stop generation (Esc)">
              <svg width="10" height="10" viewBox="0 0 10 10" aria-hidden="true">
                <rect x="1" y="1" width="8" height="8" rx="1.5" fill="currentColor" />
              </svg>
            </button>
            <span className="oc-stop-hint">Esc to interrupt</span>
          </>
        )}
      </span>
      <span className="oc-composer-footer-right">
        {tokenStats && (
          <span className="oc-session-cost" title="Session cost (reported, estimated when unavailable)">
            {formatCurrency(effectiveCost)}
          </span>
        )}
        {contextTokens != null && contextTokens > 0 && (
          <span className="oc-context-usage-wrap" ref={tokenPopoverRef}>
            <button
              type="button"
              className={`oc-context-usage${contextPercent != null && contextPercent > 80 ? ' oc-context-warn' : ''}`}
              title="Click for token usage details"
              onClick={() => setShowTokenPopover((value) => !value)}
            >
              {formatTokenCount(contextTokens)}{contextPercent != null && ` (${contextPercent.toFixed(0)}%)`}
            </button>
            {showTokenPopover && tokenStats && (
              <div className="oc-token-popover">
                <div className="oc-token-popover-title">Session token usage</div>
                <div className="oc-token-popover-rows">
                  <div className="oc-token-popover-row"><span className="oc-token-popover-label">Input</span><span className="oc-token-popover-value">{tokenStats.input.toLocaleString()}</span></div>
                  <div className="oc-token-popover-row"><span className="oc-token-popover-label">Output</span><span className="oc-token-popover-value">{tokenStats.output.toLocaleString()}</span></div>
                  {tokenStats.reasoning > 0 && <div className="oc-token-popover-row"><span className="oc-token-popover-label">Reasoning</span><span className="oc-token-popover-value">{tokenStats.reasoning.toLocaleString()}</span></div>}
                  {(tokenStats.cacheRead > 0 || tokenStats.cacheWrite > 0) && <div className="oc-token-popover-row"><span className="oc-token-popover-label">Cache read</span><span className="oc-token-popover-value">{tokenStats.cacheRead.toLocaleString()}</span></div>}
                  {tokenStats.cacheWrite > 0 && <div className="oc-token-popover-row"><span className="oc-token-popover-label">Cache write</span><span className="oc-token-popover-value">{tokenStats.cacheWrite.toLocaleString()}</span></div>}
                  <div className="oc-token-popover-divider" />
                  <div className="oc-token-popover-row">
                    <span className="oc-token-popover-label">Context used</span>
                    <span className="oc-token-popover-value">{contextTokens.toLocaleString()}{contextWindow ? ` / ${contextWindow.toLocaleString()}` : ''}{contextPercent != null ? ` (${contextPercent.toFixed(0)}%)` : ''}</span>
                  </div>
                  {tokenStats.totalCost > 0 && <div className="oc-token-popover-row"><span className="oc-token-popover-label">Reported cost</span><span className="oc-token-popover-value">${tokenStats.totalCost.toFixed(4)}</span></div>}
                  <div className="oc-token-popover-divider" />
                  <div className="oc-token-popover-row oc-token-popover-cost">
                    <span className="oc-token-popover-label">Est. cost</span>
                    <span className="oc-token-popover-value">{estCostLoading ? '…' : estCost ? (estCost.known ? `$${estCost.cost.toFixed(4)}` : 'unknown model') : 'n/a'}</span>
                  </div>
                  {sessionTreeStats && sessionTreeStats.sessions > 1 && (
                    <>
                      <div className="oc-token-popover-divider" />
                      <div className="oc-token-popover-subtitle">Session + subagents ({sessionTreeStats.sessions})</div>
                      <div className="oc-token-popover-row"><span className="oc-token-popover-label">Input</span><span className="oc-token-popover-value">{sessionTreeStats.input.toLocaleString()}</span></div>
                      <div className="oc-token-popover-row"><span className="oc-token-popover-label">Output</span><span className="oc-token-popover-value">{sessionTreeStats.output.toLocaleString()}</span></div>
                      <div className="oc-token-popover-row"><span className="oc-token-popover-label">Reported cost</span><span className="oc-token-popover-value">${sessionTreeStats.totalCost.toFixed(4)}</span></div>
                      <div className="oc-token-popover-row"><span className="oc-token-popover-label">Est. cost</span><span className="oc-token-popover-value">${sessionTreeStats.totalEstCost.toFixed(4)}</span></div>
                      <div className="oc-token-popover-row"><span className="oc-token-popover-label">Cost</span><span className="oc-token-popover-value">${sessionTreeStats.totalEffectiveCost.toFixed(4)}</span></div>
                    </>
                  )}
                </div>
              </div>
            )}
          </span>
        )}
        {visibleDurationMs > 0 && <span title="Total time spent answering">{formatDuration(visibleDurationMs)}</span>}
        {!isRunning && <button type="button" className="oc-keybind-hint" onClick={openShortcuts}>{isMacPlatform() ? '⌥+?' : 'Alt+?'} for shortcuts</button>}
      </span>
    </div>
  );
}

interface ComposerToolbarProps {
  isBashMode: boolean;
  uiDisabled: boolean;
  disabled?: boolean;
  disabledHint?: string;
  effectiveAgent: string;
  agentsLoaded?: boolean;
  agents?: AgentInfo[];
  openAgentPicker: () => void;
  hasModels: boolean;
  modelUnavailable: boolean;
  openModelPicker: () => void;
  modelButtonLabel: string;
  effectiveModel: string;
  hasReasoning: boolean;
  openReasoningPicker: () => void;
  selectedReasoning?: string;
  permissionControl?: ReactNode;
  onLaunchRequest?: () => void;
  launching?: boolean;
  fileInputRef: React.RefObject<HTMLInputElement | null>;
  addFiles: (files: File[]) => void;
  isDictationSupported: boolean;
  micRef: React.RefObject<HTMLButtonElement | null>;
  handleMicClick: () => void;
  micError: string | null;
  clearMicError: () => void;
  isRunning: boolean;
  onAbort?: () => void;
  sending: boolean;
  submit: (queue: boolean) => void;
}

function ComposerToolbar({
  isBashMode,
  uiDisabled,
  disabled,
  disabledHint,
  effectiveAgent,
  agentsLoaded,
  agents,
  openAgentPicker,
  hasModels,
  modelUnavailable,
  openModelPicker,
  modelButtonLabel,
  effectiveModel,
  hasReasoning,
  openReasoningPicker,
  selectedReasoning,
  permissionControl,
  onLaunchRequest,
  launching,
  fileInputRef,
  addFiles,
  isDictationSupported,
  micRef,
  handleMicClick,
  micError,
  clearMicError,
  isRunning,
  onAbort,
  sending,
  submit,
}: ComposerToolbarProps) {
  return (
    <div className="oc-composer-bar">
      <div className="oc-composer-bar-left">
        {isBashMode ? (
          <span className="oc-bar-shell">shell</span>
        ) : (
          <>
            <button type="button" className="oc-bar-select" disabled={uiDisabled} onClick={openAgentPicker} title="Agent (click to change)">
              {effectiveAgent && agentsLoaded && <span className="oc-agent-swatch" aria-hidden="true" style={{ background: agentColor(effectiveAgent, agents) }} />}
              {effectiveAgent || 'Agent'}
            </button>
            {hasModels && (
              <button
                type="button"
                className={`oc-bar-select${modelUnavailable ? ' oc-bar-select--warn' : ''}`}
                disabled={uiDisabled}
                onClick={openModelPicker}
                title={modelUnavailable ? 'Model not available on this host — pick another' : 'Model (click to change)'}
              >
                {modelUnavailable && <i className="bi bi-exclamation-triangle-fill" aria-hidden="true" />}
                {modelButtonLabel ? <ModelLabel model={effectiveModel}>{modelButtonLabel}</ModelLabel> : 'Model'}
              </button>
            )}
            {hasReasoning && (
              <button
                type="button"
                className="oc-bar-select oc-bar-reasoning"
                disabled={uiDisabled}
                onClick={openReasoningPicker}
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
                onClick={(event) => { event.stopPropagation(); onLaunchRequest(); }}
                disabled={launching}
                title={disabledHint || 'Launch the agent process'}
              >
                {launching ? 'Launching…' : 'Launch session'}
              </button>
            ) : disabled ? (
              <span className="oc-bar-hint" title={disabledHint || undefined}>No live connection</span>
            ) : null}
          </>
        )}
      </div>
      <div className="oc-composer-bar-right">
        <button className="oc-bar-action" onClick={() => fileInputRef.current?.click()} disabled={uiDisabled} title="Attach file">+</button>
        <input
          ref={fileInputRef}
          type="file"
          multiple
          style={{ display: 'none' }}
          onChange={(event) => {
            addFiles(Array.from(event.target.files || []));
            event.target.value = '';
          }}
        />
        {isDictationSupported && (
          <button ref={micRef} className="oc-bar-action" onClick={handleMicClick} disabled={disabled} title="Record voice message">
            <i className="bi bi-mic-fill oc-mic-icon" aria-hidden="true" />
          </button>
        )}
        {micError && (
          <span className="oc-mic-error" role="alert">
            {micError}
            <button type="button" className="oc-mic-error-dismiss" onClick={clearMicError} aria-label="Dismiss">×</button>
          </span>
        )}
        {isRunning ? (
          <button type="button" className="oc-bar-send oc-bar-send-stop" onClick={onAbort} title="Stop generation (Esc)" aria-label="Stop generation">
            <svg width="12" height="12" viewBox="0 0 10 10" aria-hidden="true"><rect x="1" y="1" width="8" height="8" rx="1.5" fill="currentColor" /></svg>
          </button>
        ) : (
          <button
            type="button"
            className={`oc-bar-send${sending ? ' oc-bar-send-sending' : ''}`}
            disabled={uiDisabled}
            title={sending ? 'Sending message' : 'Send (Enter) · Queue for next idle (Ctrl+Enter)'}
            aria-label={sending ? 'Sending message' : 'Send message'}
            onClick={(event) => submit(event.ctrlKey || event.metaKey)}
          >
            {sending
              ? <span className="oc-spinner oc-bar-send-spinner" aria-hidden="true" />
              : <svg width="16" height="16" viewBox="0 0 16 16" aria-hidden="true"><path d="M8 13V3M8 3L4 7M8 3l4 4" stroke="currentColor" strokeWidth="1.6" fill="none" strokeLinecap="round" strokeLinejoin="round" /></svg>}
          </button>
        )}
      </div>
    </div>
  );
}

function ComposerImpl({
  onSend,
  onRetryChange,
  onCommand,
  onShell,
  shellExec,
  queuedShellCommand,
  onCancelQueuedShell,
  queuedMessages,
  onRemoveQueuedMessage,
  onMoveQueuedMessage,
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
  activeDurationMs,
  sessionId,
  tokensPerSecond,
  tokenStats,
  sessionTreeStats,
  selectedReasoning,
  onReasoningChange,
  disabledHint,
  onLaunchRequest,
  launching,
  directory,
  newConversation,
  worktreesSupported,
  permissionControl,
  composerRef,
}: {
  /**
   * Submit a prompt. `queue` is true when the user pressed
   * Ctrl/Cmd+Enter — hold the prompt for the session's next idle edge
   * instead of sending it into the running turn.
   */
  onSend?: (text: string, images?: AttachedImage[], queue?: boolean) => void | Promise<void>;
  onRetryChange?: (delaySeconds: number | null) => void;
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
   * without a shell-tool primitive.
   */
  shellExec?: boolean;
  /**
   * A `!`-prefixed shell command that was submitted while the agent
   * was streaming and is now waiting for the turn to finish before it
   * runs. Shown in the follow-up queue. Null when nothing is queued.
   */
  queuedShellCommand?: string | null;
  /** Drop the queued shell command without running it. */
  onCancelQueuedShell?: () => void;
  /**
   * Follow-up prompts the user explicitly queued with Ctrl/Cmd+Enter
   * (#58). They send one per turn as the session goes idle. Shown as a
   * list under the composer with remove / reorder controls. Empty /
   * undefined → nothing rendered.
   */
  queuedMessages?: { id: string; text: string; hasImages: boolean }[];
  /** Remove a queued follow-up without sending it. */
  onRemoveQueuedMessage?: (id: string) => void;
  /** Reorder a queued follow-up up (-1) or down (+1). */
  onMoveQueuedMessage?: (id: string, direction: -1 | 1) => void;
  onAbort?: () => void;
  isRunning: boolean;
  disabled?: boolean;
  /**
   * User-facing hint explaining *why* the composer is disabled and
   * how to fix it (e.g. "Launch a session to start OpenCode ..."
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
  activeDurationMs?: number;
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
  sessionTreeStats?: {
    input: number;
    output: number;
    totalCost: number;
    totalEstCost: number;
    totalEffectiveCost: number;
    sessions: number;
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
  composerRef?: Ref<ComposerHandle>;
}) {
  const [displayDurationMs, setDisplayDurationMs] = useState(activeDurationMs ?? 0);

  const wrapRef = useRef<HTMLDivElement>(null);
  const inputRef = useRef<HTMLTextAreaElement>(null);
  const fileInputRef = useRef<HTMLInputElement>(null);
  const [images, setImages] = useState<AttachedImage[]>([]);
  const [files, setFiles] = useState<AttachedFileRef[]>([]);
  // Keep the draft locked until the send succeeds. Backend outages retry;
  // other errors unlock the unchanged draft for manual correction/retry.
  const [sending, setSending] = useState(false);
  const sendingRef = useRef(false);
  const mountedRef = useRef(true);
  const sessionIdRef = useRef(sessionId);
  const { clearDraftNow, scheduleDraftSave } = useComposerDrafts(inputRef, sessionId, sessionIdRef);

  useEffect(() => {
    const baseDurationMs = activeDurationMs ?? 0;
    if (!isRunning) return;

    const startedAt = Date.now();
    const updateDuration = () => {
      setDisplayDurationMs(baseDurationMs + Date.now() - startedAt);
    };
    const initialTimer = window.setTimeout(updateDuration, 0);
    const timer = window.setInterval(updateDuration, 1_000);
    return () => {
      window.clearTimeout(initialTimer);
      window.clearInterval(timer);
    };
  }, [activeDurationMs, isRunning]);

  const visibleDurationMs = isRunning ? displayDurationMs : (activeDurationMs ?? 0);

  useEffect(() => { sessionIdRef.current = sessionId; }, [sessionId]);
  useEffect(() => { sendingRef.current = sending; }, [sending]);
  useEffect(() => {
    mountedRef.current = true;
    return () => { mountedRef.current = false; };
  }, []);

  // Put the caret back after a send.
  //
  // The textarea is `disabled` while sending (uiDisabled), and disabling a
  // focused control blurs it — re-enabling does not put focus back, so
  // after pressing Enter the caret was gone and the next keystroke went
  // nowhere. Restoring has to wait for the commit that clears `disabled`,
  // because focus() on a disabled element is a no-op; an effect keyed on
  // `sending` is exactly that moment.
  //
  // Two conditions, both required. The composer must have owned focus when
  // the send started — a send can also come from the send button or a slash
  // command while the user is elsewhere, and stealing focus there would be
  // worse than losing it. And focus must still be on the floor: the disable
  // drops it to <body>, so anything else means the user deliberately moved
  // during the send and we leave them where they are.
  const restoreFocusAfterSendRef = useRef(false);
  useEffect(() => {
    if (sending || !restoreFocusAfterSendRef.current) return;
    restoreFocusAfterSendRef.current = false;
    const el = inputRef.current;
    if (!el || el.disabled) return;
    const active = document.activeElement;
    if (active && active !== document.body) return;
    el.focus({ preventScroll: true });
  }, [sending]);

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

  const [slashCommands, setSlashCommands] = useState<SlashCommand[]>(BUILTIN_COMMANDS);
  const [showSlashMenu, setShowSlashMenu] = useState(false);
  const [slashFilter, setSlashFilter] = useState('');
  const [slashIndex, setSlashIndex] = useState(0);
  const [isBashMode, setIsBashMode] = useState(false);
  const [modelPickerOpen, setModelPickerOpen] = useState(false);
  const [modelPickerQuery, setModelPickerQuery] = useState('');
  const [agentPickerOpen, setAgentPickerOpen] = useState(false);
  const [agentPickerQuery, setAgentPickerQuery] = useState('');
  const [skillPickerOpen, setSkillPickerOpen] = useState(false);
  const [skillPickerQuery, setSkillPickerQuery] = useState('');
  const [reasoningPickerOpen, setReasoningPickerOpen] = useState(false);
  const [helpOpen, setHelpOpen] = useState(false);
  const slashMenuRef = useRef<HTMLDivElement>(null);

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
  // Agent picker has something to show as long as the live catalog has an agent.
  const hasAgents = !!(agents && agents.length > 0);
  const cyclableAgents = hasAgents
    ? (agents || []).filter((a) => a.mode !== 'subagent' && !a.hidden).map((a) => a.name)
    : KNOWN_AGENTS;
  const agentOptions = Array.from(new Set([activeAgent, ...cyclableAgents].filter((a): a is string => !!a)));
  const effectiveAgent = selectedAgent || activeAgent || '';
  const hasSkills = slashCommands.some(c => c.source === 'skill');
  // Variants == the reasoning options the effective model exposes, so the
  // /variants command is hidden when the model has none (OpenCode parity).
  const hasVariants = modelHasVariants(selectedModel, modelEntries);
  const filteredCommands = slashCommands.filter(cmd => {
    if (cmd.name === 'model' && !hasModels) return false;
    if ((cmd.name === 'agent' || cmd.name === 'agents') && !hasAgents && (!activeAgent)) return false;
    if (cmd.name === 'skills' && !hasSkills) return false;
    // Mirror OpenCode: /variants is hidden when the model exposes no variants.
    if (cmd.name === 'variants' && !hasVariants) return false;
    return cmd.name.toLowerCase().startsWith(slashFilter.toLowerCase());
  });

  useEffect(() => {
    if (!showSlashMenu || !slashMenuRef.current) return;
    const active = slashMenuRef.current.querySelector('.oc-slash-item.active');
    if (active) active.scrollIntoView({ block: 'nearest' });
  }, [slashIndex, showSlashMenu]);

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

  useImperativeHandle(composerRef, () => ({
    openModelPicker,
    openAgentPicker,
  }), [openModelPicker, openAgentPicker]);

  const openSkillPicker = useCallback((arg: string) => {
    setSkillPickerQuery(arg);
    setSkillPickerOpen(true);
  }, []);

  // On skill select: prefill `/<skill> ` into the composer — do not send.
  // Matches OpenCode's DialogSkill, which inserts the command for the user
  // to complete/submit themselves.
  const insertSkill = useCallback((skill: string) => {
    setSkillPickerOpen(false);
    const el = inputRef.current;
    if (!el) return;
    el.value = '/' + skill + ' ';
    el.focus();
    const sid = sessionIdRef.current;
    if (sid) scheduleDraftSave(sid, () => el.value);
  }, [scheduleDraftSave]);

  const clearComposerInput = useCallback(() => {
    const el = inputRef.current;
    if (!el) return;
    el.value = '';
    setShowSlashMenu(false);
    setSlashFilter('');
    setSlashIndex(0);
    const sid = sessionIdRef.current;
    if (sid) clearDraftNow(sid);
  }, [clearDraftNow]);

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
    // `/agents` is an OpenCode-parity alias for `/agent` (#295).
    if (cmd.name === 'agent' || cmd.name === 'agents') {
      clearComposerInput();
      openAgentPicker('');
      return;
    }
    if (cmd.name === 'help') {
      clearComposerInput();
      setHelpOpen(true);
      return;
    }
    if (cmd.name === 'skills') {
      clearComposerInput();
      openSkillPicker('');
      return;
    }
    // /variants reuses the reasoning picker: it tunes the current model's
    // reasoning variant. Hidden from the menu when the model has none.
    if (cmd.name === 'variants') {
      clearComposerInput();
      setReasoningPickerOpen(true);
      return;
    }
    el.value = '/' + cmd.name + ' ';
    el.focus();
    setShowSlashMenu(false);
    setSlashFilter('');
    setSlashIndex(0);
  }, [clearComposerInput, openModelPicker, openAgentPicker, openSkillPicker]);

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

  const closeSlashMenu = () => {
    setShowSlashMenu(false);
    setSlashFilter('');
    setSlashIndex(0);
  };

  const clearAfterSubmit = () => {
    if (inputRef.current) inputRef.current.value = '';
    closeSlashMenu();
    setIsBashMode(false);
    setImages([]);
    setFiles([]);
    const sid = sessionIdRef.current;
    if (sid) clearDraftNow(sid);
  };

  const runSend = async (text: string, imgs?: AttachedImage[], queue?: boolean) => {
    const el = inputRef.current;
    if (!el) return;
    const hadFocus = document.activeElement === el;
    sendingRef.current = true;
    setSending(true);
    onRetryChange?.(null);
    let retries = 0;
    const MAX_BACKEND_RETRIES = 5;
    while (mountedRef.current) {
      try {
        await onSend?.(text, imgs, queue);
        clearAfterSubmit();
        break;
      } catch (err) {
        if (!(err instanceof BackendUnavailableError) || retries >= MAX_BACKEND_RETRIES) break;
        retries += 1;
        const delaySeconds = 2 ** (retries - 1);
        onRetryChange?.(delaySeconds);
        await new Promise(resolve => window.setTimeout(resolve, 1_000 * delaySeconds));
      }
    }
    if (mountedRef.current) {
      onRetryChange?.(null);
      sendingRef.current = false;
      restoreFocusAfterSendRef.current = hadFocus;
      setSending(false);
    }
  };

  const openClientCommand = (command: string, args: string) => {
    if (!['model', 'agent', 'agents', 'help', 'skills'].includes(command)) return false;
    clearComposerInput();
    setIsBashMode(false);
    if (command === 'model') openModelPicker(args);
    else if (command === 'agent' || command === 'agents') openAgentPicker(args);
    else if (command === 'help') setHelpOpen(true);
    else openSkillPicker(args);
    return true;
  };

  const submit = (queue: boolean) => {
    const raw = inputRef.current?.value ?? '';
    const route = routeComposerSubmit(raw, { shellExec: !!shellExec });
    if (route.kind === 'noop' && images.length === 0 && files.length === 0) return;

    const fileReferenceText = files.length > 0
      ? `Attached files saved on disk:\n${files.map(f => `- ${f.path} (${f.mime})`).join('\n')}`
      : '';
    const withFileReferences = (text: string) => [text, fileReferenceText].filter(Boolean).join('\n\n');

    if (route.kind === 'command' && onCommand) {
      if (openClientCommand(route.command, route.args)) return;
      onCommand(route.command, route.args);
      clearAfterSubmit();
    } else if (route.kind === 'shell' && onShell) {
      onShell(route.command);
      clearAfterSubmit();
    } else {
      const text = route.kind === 'send' ? route.text : route.kind === 'noop' ? '' : raw.trim();
      void runSend(withFileReferences(text), images.length > 0 ? images : undefined, queue);
    }
  };

  const handleInputKeyDown = (e: React.KeyboardEvent<HTMLTextAreaElement>) => {
    if (disabled || sendingRef.current) return;
    const el = e.currentTarget;
    const hasArg = el.value.includes(' ');

    if (showSlashMenu && !hasArg && e.key === 'ArrowDown') {
      e.preventDefault();
      setSlashIndex((slashIndex + 1) % Math.max(filteredCommands.length, 1));
      return;
    }
    if (showSlashMenu && !hasArg && e.key === 'ArrowUp') {
      e.preventDefault();
      setSlashIndex((slashIndex - 1 + Math.max(filteredCommands.length, 1)) % Math.max(filteredCommands.length, 1));
      return;
    }
    if (showSlashMenu && e.key === 'Escape') {
      e.preventDefault();
      closeSlashMenu();
      return;
    }
    if (showSlashMenu && !hasArg && (e.key === 'Tab' || (e.key === 'Enter' && !e.shiftKey))) {
      const cmd = filteredCommands[slashIndex];
      if (cmd) {
        e.preventDefault();
        selectSlashCommand(cmd);
      }
      return;
    }
    if (e.key === 'Tab' && !e.ctrlKey && !e.metaKey && !e.altKey && agentOptions.length > 0 && onAgentChange) {
      e.preventDefault();
      const index = agentOptions.indexOf(effectiveAgent);
      const direction = e.shiftKey ? -1 : 1;
      const nextIndex = index === -1
        ? (e.shiftKey ? agentOptions.length - 1 : 0)
        : (index + direction + agentOptions.length) % agentOptions.length;
      onAgentChange(agentOptions[nextIndex]);
      return;
    }
    if (e.key === 'c' && e.ctrlKey && el.value.trim() && el.selectionStart === el.selectionEnd) {
      e.preventDefault();
      clearAfterSubmit();
      return;
    }
    if (e.key === 'Enter' && !e.shiftKey) {
      e.preventDefault();
      submit(e.ctrlKey || e.metaKey);
    }
  };

  const handleInput = (e: React.FormEvent<HTMLTextAreaElement>) => {
    const el = e.currentTarget;
    const value = el.value;
    setIsBashMode(value.startsWith('!') && !!shellExec);
    const show = value.startsWith('/') && !value.includes(' ') && !value.includes('\n');
    setShowSlashMenu(show);
    setSlashFilter(show ? value.slice(1) : '');
    if (!show) setSlashIndex(0);
    const sid = sessionIdRef.current;
    if (sid) scheduleDraftSave(sid, () => el.value);
  };

  const handlePaste = (e: React.ClipboardEvent<HTMLTextAreaElement>) => {
    if (disabled) return;
    const imageFiles = Array.from(e.clipboardData.items)
      .filter((item) => item.type.startsWith('image/'))
      .map((item) => item.getAsFile())
      .filter((file): file is File => !!file);
    if (imageFiles.length === 0) return;
    e.preventDefault();
    void addImageFiles(imageFiles);
  };

  useEffect(() => {
    const handleKeyDown = (e: KeyboardEvent) => {
      if (e.defaultPrevented) return;
      if (e.key === 'Escape' && isRunning && onAbort) {
        e.preventDefault();
        onAbort();
      }
    };
    window.addEventListener('keydown', handleKeyDown);
    return () => window.removeEventListener('keydown', handleKeyDown);
  }, [isRunning, onAbort]);

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

  const effectiveModel = selectedModel || '';

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
  // Warn when the selected model isn't available on this session's host
  // (e.g. provider not connected on a remote). Only trust the rich entries;
  // the string fallback has no availability data so we stay silent there.
  const modelUnavailable = (() => {
    if (!effectiveModel || !modelEntries || modelEntries.length === 0) return false;
    const match = modelEntries.find((e) => e.provider && `${e.provider}/${e.model}` === effectiveModel);
    return !!match && !match.isAvailable;
  })();
  // Reasoning options for the effective model, derived from the model entries.
  const reasoningOptions: string[] = (() => {
    if (!effectiveModel || !modelEntries) return [];
    const match = modelEntries.find((e) => e.provider && `${e.provider}/${e.model}` === effectiveModel);
    return match?.reasoning ?? [];
  })();
  const hasReasoning = reasoningOptions.length > 0;
  const dictationShortcut = useMemo(() => ({
    id: 'composer.dictation',
    scope: 'composer' as const,
    keys: { code: 'KeyD', alt: true },
    description: 'Start dictation (voice input)',
    enabled: () => !!(isDictationSupported && !isRecording && !disabled),
    handler: () => { void handleMicClick(); },
  }), [isDictationSupported, isRecording, disabled, handleMicClick]);

  const reasoningCycleShortcut = useMemo(() => ({
    id: 'composer.reasoning-cycle',
    scope: 'composer' as const,
    keys: { code: 'KeyR', alt: true },
    description: 'Cycle reasoning level',
    enabled: () => !!(reasoningOptions.length > 0 && !disabled),
    handler: () => {
      const opts = reasoningOptions;
      if (opts.length === 0) return;
      if (!onReasoningChange) return;
      const cycle = ['', ...opts];
      const cur = cycle.indexOf(selectedReasoning ?? '');
      const next = cycle[(cur + 1) % cycle.length];
      onReasoningChange(next);
    },
  }), [reasoningOptions, disabled, onReasoningChange, selectedReasoning]);

  useShortcut(dictationShortcut);
  useShortcut(reasoningCycleShortcut);

  // While a send is in flight, disable the interactive send controls
  // (input, selectors, send button) so a second send can't fire before the
  // first round-trip completes. The launch-overlay / hint logic below stays
  // keyed on the real `disabled` prop (a live-connection state, not this
  // transient send block).
  const uiDisabled = disabled || sending;
  const shellQueueID = '__shell__';
  const queueItems = [
    ...(queuedMessages || []),
    ...(queuedShellCommand ? [{
      id: shellQueueID,
      text: `!${queuedShellCommand}`,
      hasImages: false,
      canMove: false,
      removeLabel: 'Cancel queued shell command',
    }] : []),
  ];

  return (
    <>
    <div
      className={`oc-composer-wrap${uiDisabled ? ' oc-composer-disabled' : ''}`}
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
      {skillPickerOpen && (
        <SkillPicker
          open={skillPickerOpen}
          commands={slashCommands}
          initialQuery={skillPickerQuery}
          onSelect={insertSkill}
          onClose={() => { setSkillPickerOpen(false); inputRef.current?.focus(); }}
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
      {helpOpen && (
        <HelpDialog
          open={helpOpen}
          commands={slashCommands}
          onClose={() => { setHelpOpen(false); inputRef.current?.focus(); }}
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
      {!disabled && (
        <QueuedMessages
          messages={queueItems}
          onRemove={(id) => id === shellQueueID ? onCancelQueuedShell?.() : onRemoveQueuedMessage?.(id)}
          onMove={onMoveQueuedMessage}
        />
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
          disabled={uiDisabled}
          placeholder={disabled ? (disabledHint || 'No live connection to the agent') : undefined}
          autoComplete="off"
          autoCorrect="off"
          autoCapitalize="off"
          spellCheck={false}
          onKeyDown={handleInputKeyDown}
          onInput={handleInput}
          onPaste={handlePaste}
          data-1p-ignore
          data-lpignore="true"
          data-bwignore
          data-form-type="other"
        />
        <ComposerToolbar
          isBashMode={isBashMode}
          uiDisabled={uiDisabled}
          disabled={disabled}
          disabledHint={disabledHint}
          effectiveAgent={effectiveAgent}
          agentsLoaded={agentsLoaded}
          agents={agents}
          openAgentPicker={() => { if (!uiDisabled) openAgentPicker(); }}
          hasModels={hasModels}
          modelUnavailable={modelUnavailable}
          openModelPicker={() => {
            if (uiDisabled) return;
            openModelPicker();
          }}
          modelButtonLabel={modelButtonLabel}
          effectiveModel={effectiveModel}
          hasReasoning={hasReasoning}
          openReasoningPicker={() => { if (!uiDisabled) setReasoningPickerOpen(true); }}
          selectedReasoning={selectedReasoning}
          permissionControl={permissionControl}
          onLaunchRequest={onLaunchRequest}
          launching={launching}
          fileInputRef={fileInputRef}
          addFiles={(selectedFiles) => { void addImageFiles(selectedFiles); }}
          isDictationSupported={isDictationSupported}
          micRef={micRef}
          handleMicClick={() => { void handleMicClick(); }}
          micError={micError}
          clearMicError={() => setMicError(null)}
          isRunning={isRunning}
          onAbort={onAbort}
          sending={sending}
          submit={submit}
        />
      </div>
      <ComposerFooter
        directory={directory}
        newConversation={newConversation}
        worktreesSupported={worktreesSupported}
        sessionId={sessionId}
        disabled={disabled}
        isRunning={isRunning}
        effectiveAgent={effectiveAgent}
        agentsLoaded={agentsLoaded}
        agents={agents}
        tokensPerSecond={tokensPerSecond}
        onAbort={onAbort}
        tokenStats={tokenStats}
        sessionTreeStats={sessionTreeStats}
        contextTokens={contextTokens}
        effectiveModel={effectiveModel}
        visibleDurationMs={visibleDurationMs}
      />
    </div>
    </>
  );
}

export const Composer = ComposerImpl;
