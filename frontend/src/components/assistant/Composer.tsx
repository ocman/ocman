import { useState, useEffect, useRef, useCallback, useMemo, useImperativeHandle, type ReactNode, type Ref } from 'react';
import './Composer.css';
import { useComposerDrafts } from './useComposerDrafts';
import { isMacPlatform } from '../../lib/shortcuts';
import { useShortcut } from '../../lib/shortcutRegistry';
import { useUiStore } from '../../lib/uiStore';
import { BackendUnavailableError, type SlashCommand, type AgentInfo, type SessionModelEntry } from '../../lib/api';
import { useComposerAttachments, type AttachedImage } from './useComposerAttachments';
import { useSlashMenu } from './useSlashMenu';
import { useComposerPickers } from './useComposerPickers';
import { useRunningDuration } from './useRunningDuration';
import { describeModel } from './composerModel';
import { agentColor } from '../../lib/agentColor';
import { ModelPicker } from './ModelPicker';
import { AgentPicker } from './AgentPicker';
import { SkillPicker } from './SkillPicker';
import { RoutinePicker } from './RoutinePicker';
import { ReasoningPicker } from './ReasoningPicker';
import { HelpDialog } from './HelpDialog';
import { useClickOutside } from '../../lib/useClickOutside';
import { TargetSelector } from './ComposerSelectorRow';
import { SlashCommandMenu } from './SlashCommandMenu';
import { QueuedMessages } from './QueuedMessages';
import { useComposerAudio } from './useComposerAudio';
import { routeComposerSubmit } from './composerSubmit';
import { getContextWindow, formatTokenCount } from '../../lib/models/contextWindows';
import { formatCurrency, formatDate, formatDuration, formatTokensPerSecond } from '../../lib/format';
import { KNOWN_AGENTS, modelHasVariants } from '../../lib/commands/builtinCommands';
import { ModelLabel } from '../ModelLogo';
import { ModalReturnFocusContext } from '../ModalReturnFocusContext';

export type { AttachedImage } from './useComposerAttachments';

export interface ComposerHandle {
  openModelPicker: (query?: string) => void;
  openAgentPicker: (query?: string) => void;
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
  estimatedCost?: number;
  sessionTreeStats?: { input: number; output: number; totalCost: number; totalEstCost: number; totalEffectiveCost: number; sessions: number };
  contextTokens?: number;
  effectiveModel: string;
  timeCreated?: number;
  durationMs?: number;
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
  estimatedCost,
  sessionTreeStats,
  contextTokens,
  effectiveModel,
  timeCreated,
  durationMs,
  visibleDurationMs,
}: ComposerFooterProps) {
  const [showTokenPopover, setShowTokenPopover] = useState(false);
  const tokenPopoverRef = useRef<HTMLDivElement>(null);
  const openShortcuts = useUiStore((s) => s.openShortcuts);
  const contextWindow = contextTokens ? getContextWindow(effectiveModel) : null;
  const contextPercent = contextTokens && contextWindow
    ? Math.min(100, (contextTokens / contextWindow) * 100)
    : null;

  useClickOutside(tokenPopoverRef, showTokenPopover, () => setShowTokenPopover(false));

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
          <span className="oc-context-usage-wrap" ref={tokenPopoverRef}>
            <button type="button" className="oc-session-cost" title="Click for cost and token usage details" onClick={() => setShowTokenPopover((value) => !value)}>
              {formatCurrency(effectiveCost, 2)}
            </button>
            {contextTokens != null && contextTokens > 0 && (
              <button
                type="button"
                className={`oc-context-usage${contextPercent != null && contextPercent > 80 ? ' oc-context-warn' : ''}`}
                title="Click for token usage details"
                onClick={() => setShowTokenPopover((value) => !value)}
              >
                {formatTokenCount(contextTokens)}{contextPercent != null && ` (${contextPercent.toFixed(0)}%)`}
              </button>
            )}
            {showTokenPopover && (
              <div className="oc-token-popover">
                <div className="oc-token-popover-title">Session token usage</div>
                <div className="oc-token-popover-rows">
                  <div className="oc-token-popover-row"><span className="oc-token-popover-label">Input</span><span className="oc-token-popover-value">{tokenStats.input.toLocaleString()}</span></div>
                  <div className="oc-token-popover-row"><span className="oc-token-popover-label">Output</span><span className="oc-token-popover-value">{tokenStats.output.toLocaleString()}</span></div>
                  {tokenStats.reasoning > 0 && <div className="oc-token-popover-row"><span className="oc-token-popover-label">Reasoning</span><span className="oc-token-popover-value">{tokenStats.reasoning.toLocaleString()}</span></div>}
                  {(tokenStats.cacheRead > 0 || tokenStats.cacheWrite > 0) && <div className="oc-token-popover-row"><span className="oc-token-popover-label">Cache read</span><span className="oc-token-popover-value">{tokenStats.cacheRead.toLocaleString()}</span></div>}
                  {tokenStats.cacheWrite > 0 && <div className="oc-token-popover-row"><span className="oc-token-popover-label">Cache write</span><span className="oc-token-popover-value">{tokenStats.cacheWrite.toLocaleString()}</span></div>}
                  {contextTokens != null && contextTokens > 0 && (
                    <>
                      <div className="oc-token-popover-divider" />
                      <div className="oc-token-popover-row">
                        <span className="oc-token-popover-label">Context used</span>
                        <span className="oc-token-popover-value">{contextTokens.toLocaleString()}{contextWindow ? ` / ${contextWindow.toLocaleString()}` : ''}{contextPercent != null ? ` (${contextPercent.toFixed(0)}%)` : ''}</span>
                      </div>
                    </>
                  )}
                  {tokenStats.totalCost > 0 && <div className="oc-token-popover-row"><span className="oc-token-popover-label">Reported cost</span><span className="oc-token-popover-value">${tokenStats.totalCost.toFixed(4)}</span></div>}
                  <div className="oc-token-popover-divider" />
                  <div className="oc-token-popover-row oc-token-popover-cost">
                    <span className="oc-token-popover-label">Est. cost</span>
                    <span className="oc-token-popover-value">{estimatedCost != null ? `$${estimatedCost.toFixed(4)}` : 'n/a'}</span>
                  </div>
                  {timeCreated != null && (
                    <>
                      <div className="oc-token-popover-divider" />
                      <div className="oc-token-popover-subtitle">Timing</div>
                      <div className="oc-token-popover-row"><span className="oc-token-popover-label">Started</span><span className="oc-token-popover-value">{formatDate(timeCreated)}</span></div>
                      <div className="oc-token-popover-row"><span className="oc-token-popover-label">Total time</span><span className="oc-token-popover-value">{formatDuration(durationMs ?? 0)}</span></div>
                      <div className="oc-token-popover-row"><span className="oc-token-popover-label">Agent time</span><span className="oc-token-popover-value">{formatDuration(visibleDurationMs)}</span></div>
                    </>
                  )}
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
  timeCreated,
  durationMs,
  sessionId,
  tokensPerSecond,
  tokenStats,
  estimatedCost,
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
  timeCreated?: number;
  durationMs?: number;
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
  estimatedCost?: number;
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
  const wrapRef = useRef<HTMLDivElement>(null);
  const inputRef = useRef<HTMLTextAreaElement>(null);
  const fileInputRef = useRef<HTMLInputElement>(null);
  // Keep the draft locked until the send succeeds. Backend outages retry;
  // other errors unlock the unchanged draft for manual correction/retry.
  const [sending, setSending] = useState(false);
  const sendingRef = useRef(false);
  const mountedRef = useRef(true);
  const sessionIdRef = useRef(sessionId);
  const { clearDraftNow, scheduleDraftSave } = useComposerDrafts(inputRef, sessionId, sessionIdRef);

  const visibleDurationMs = useRunningDuration(activeDurationMs, isRunning);
  const attachments = useComposerAttachments(sessionIdRef, disabled);
  const { images, files } = attachments;

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
    const touchOnly = window.matchMedia?.('(any-pointer: coarse)').matches
      && !window.matchMedia?.('(any-pointer: fine)').matches;
    if (touchOnly) return;
    if (!disabled && inputRef.current) {
      const timer = setTimeout(() => {
        if (document.activeElement?.tagName !== 'INPUT' && document.activeElement?.tagName !== 'TEXTAREA') {
          inputRef.current?.focus();
        }
      }, 0);
      return () => clearTimeout(timer);
    }
  }, [disabled]);

  const [isBashMode, setIsBashMode] = useState(false);

  const hasModels = !!((models && models.length > 0) || (modelEntries && modelEntries.length > 0));
  // Agent picker has something to show as long as the live catalog has an agent.
  const hasAgents = !!(agents && agents.length > 0);
  const cyclableAgents = hasAgents
    ? (agents || []).filter((a) => a.mode !== 'subagent' && !a.hidden).map((a) => a.name)
    : KNOWN_AGENTS;
  const agentOptions = Array.from(new Set([activeAgent, ...cyclableAgents].filter((a): a is string => !!a)));
  const effectiveAgent = selectedAgent || activeAgent || '';
  // Variants == the reasoning options the effective model exposes, so the
  // /variants command is hidden when the model has none (OpenCode parity).
  const hasVariants = modelHasVariants(selectedModel, modelEntries);
  const slash = useSlashMenu(sessionId, { hasModels, hasAgents, activeAgent, hasVariants });
  const pickers = useComposerPickers({
    inputRef,
    sessionIdRef,
    scheduleDraftSave,
    models,
    agents,
    agentOptions,
    onModelChange,
    onAgentChange,
    onRefreshModels,
  });
  const { openModelPicker, openAgentPicker, openSkillPicker, openRoutinePicker } = pickers;

  useImperativeHandle(composerRef, () => ({
    openModelPicker,
    openAgentPicker,
  }), [openModelPicker, openAgentPicker]);

  const clearComposerInput = useCallback(() => {
    const el = inputRef.current;
    if (!el) return;
    el.value = '';
    slash.close();
    const sid = sessionIdRef.current;
    if (sid) clearDraftNow(sid);
  }, [clearDraftNow, slash]);

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
      pickers.help.setOpen(true);
      return;
    }
    if (cmd.name === 'skills') {
      clearComposerInput();
      openSkillPicker('');
      return;
    }
    if (cmd.name === 'routines') {
      clearComposerInput();
      openRoutinePicker('');
      return;
    }
    // /variants reuses the reasoning picker: it tunes the current model's
    // reasoning variant. Hidden from the menu when the model has none.
    if (cmd.name === 'variants') {
      clearComposerInput();
      pickers.reasoning.setOpen(true);
      return;
    }
    el.value = '/' + cmd.name + ' ';
    el.focus();
    slash.close();
  }, [clearComposerInput, openModelPicker, openAgentPicker, openSkillPicker, openRoutinePicker, pickers, slash]);

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

  const clearAfterSubmit = () => {
    if (inputRef.current) inputRef.current.value = '';
    slash.close();
    setIsBashMode(false);
    attachments.clear();
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
    if (!['model', 'agent', 'agents', 'help', 'skills', 'routines'].includes(command)) return false;
    clearComposerInput();
    setIsBashMode(false);
    if (command === 'model') openModelPicker(args);
    else if (command === 'agent' || command === 'agents') openAgentPicker(args);
    else if (command === 'help') pickers.help.setOpen(true);
    else if (command === 'skills') openSkillPicker(args);
    else openRoutinePicker(args);
    return true;
  };

  const submit = (queue: boolean) => {
    const raw = inputRef.current?.value ?? '';
    const route = routeComposerSubmit(raw, { shellExec: !!shellExec });
    if (route.kind === 'noop' && images.length === 0 && files.length === 0) return;

    const withFileReferences = (text: string) => [text, attachments.fileReferenceText].filter(Boolean).join('\n\n');

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

    if (slash.open && !hasArg && e.key === 'ArrowDown') {
      e.preventDefault();
      slash.moveIndex(1);
      return;
    }
    if (slash.open && !hasArg && e.key === 'ArrowUp') {
      e.preventDefault();
      slash.moveIndex(-1);
      return;
    }
    if (slash.open && e.key === 'Escape') {
      e.preventDefault();
      slash.close();
      return;
    }
    if (slash.open && !hasArg && (e.key === 'Tab' || (e.key === 'Enter' && !e.shiftKey))) {
      const cmd = slash.filtered[slash.index];
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
    slash.syncToInput(value);
    const sid = sessionIdRef.current;
    if (sid) scheduleDraftSave(sid, () => el.value);
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

  const effectiveModel = selectedModel || '';

  const { label: modelButtonLabel, unavailable: modelUnavailable, reasoningOptions } = describeModel(effectiveModel, modelEntries);
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
    <ModalReturnFocusContext value={inputRef}>
    <div
      className={`oc-composer-wrap${uiDisabled ? ' oc-composer-disabled' : ''}`}
      ref={wrapRef}
      onDragOver={attachments.handleDragOver}
      onDrop={attachments.handleDrop}
      onClick={disabled && onLaunchRequest ? onLaunchRequest : undefined}
      style={disabled && onLaunchRequest ? { cursor: 'pointer' } : undefined}
    >
      {pickers.model.open && (
        <ModelPicker
          open
          models={models || []}
          modelEntries={modelEntries}
          currentModel={effectiveModel}
          initialQuery={pickers.model.query}
          onSelect={(m) => onModelChange?.(m)}
          onToggleFavorite={onToggleFavorite}
          onClose={pickers.model.close}
          onBack={() => { pickers.model.setOpen(false); useUiStore.getState().openPalette('command'); }}
        />
      )}
      {pickers.agent.open && (
        <AgentPicker
          open
          agentNames={agentOptions}
          agents={agents}
          activeAgent={activeAgent}
          currentAgent={effectiveAgent}
          initialQuery={pickers.agent.query}
          onSelect={(a) => onAgentChange?.(a)}
          onClose={pickers.agent.close}
        />
      )}
      {pickers.skill.open && (
        <SkillPicker
          open
          commands={slash.commands}
          initialQuery={pickers.skill.query}
          onSelect={pickers.insertSkill}
          onClose={pickers.skill.close}
        />
      )}
      {pickers.routine.open && (
        <RoutinePicker
          open
          initialQuery={pickers.routine.query}
          onSelect={pickers.insertRoutine}
          onClose={pickers.routine.close}
        />
      )}
      {pickers.reasoning.open && (
        <ReasoningPicker
          open
          options={reasoningOptions}
          current={selectedReasoning}
          onSelect={(v) => onReasoningChange?.(v)}
          onClose={pickers.reasoning.close}
        />
      )}
      {pickers.help.open && (
        <HelpDialog
          open
          commands={slash.commands}
          onClose={pickers.help.close}
        />
      )}
      {slash.open && (
        <SlashCommandMenu
          commands={slash.filtered}
          activeIndex={slash.index}
          menuRef={slash.menuRef}
          onSelect={selectSlashCommand}
          onHover={slash.setIndex}
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
                <button
                  type="button"
                  className="oc-composer-image-remove"
                  title={`Remove image attachment ${i + 1}`}
                  aria-label={`Remove image attachment ${i + 1}`}
                  onClick={() => attachments.removeImage(i)}
                >
                  {'\u00D7'}
                </button>
              </div>
            ))}
            {files.map((file, i) => (
              <div key={file.path} className="oc-composer-file-thumb" title={file.path}>
                <span className="oc-composer-file-icon">file</span>
                <span className="oc-composer-file-name">{file.name}</span>
                <button
                  type="button"
                  className="oc-composer-image-remove"
                  title={`Remove attached file ${file.name}, attachment ${i + 1}`}
                  aria-label={`Remove attached file ${file.name}, attachment ${i + 1}`}
                  onClick={() => attachments.removeFile(i)}
                >
                  {'\u00D7'}
                </button>
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
          onPaste={attachments.handlePaste}
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
          openReasoningPicker={() => { if (!uiDisabled) pickers.reasoning.setOpen(true); }}
          selectedReasoning={selectedReasoning}
          permissionControl={permissionControl}
          onLaunchRequest={onLaunchRequest}
          launching={launching}
          fileInputRef={fileInputRef}
          addFiles={(selectedFiles) => { void attachments.addFiles(selectedFiles); }}
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
        estimatedCost={estimatedCost}
        sessionTreeStats={sessionTreeStats}
        contextTokens={contextTokens}
        effectiveModel={effectiveModel}
        timeCreated={timeCreated}
        durationMs={durationMs}
        visibleDurationMs={visibleDurationMs}
      />
    </div>
    </ModalReturnFocusContext>
  );
}

export const Composer = ComposerImpl;
