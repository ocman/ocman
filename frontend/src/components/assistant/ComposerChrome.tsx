import { useRef, useState, type ReactNode } from 'react';
import type { AgentInfo } from '../../lib/api';
import { useUiStore } from '../../lib/uiStore';
import { useClickOutside } from '../../lib/useClickOutside';
import { isMacPlatform } from '../../lib/shortcuts';
import { agentColor } from '../../lib/agentColor';
import { getContextWindow, formatTokenCount } from '../../lib/models/contextWindows';
import { formatCurrency, formatDate, formatDuration, formatTokensPerSecond } from '../../lib/format';
import { ModelLabel } from '../ModelLogo';
import { TargetSelector } from './ComposerSelectorRow';
import type { SessionTarget, TargetWorktree } from './ComposerSelectorRow';

interface ComposerFooterProps {
  machineControl?: ReactNode;
  directory?: string;
  newConversation?: boolean;
  worktreesSupported?: boolean;
  worktrees?: TargetWorktree[];
  sessionId?: string;
  target?: SessionTarget;
  onTargetChange?: (target: SessionTarget) => void;
  disabled?: boolean;
  targetDisabled?: boolean;
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

export function ComposerFooter({
  machineControl,
  directory, newConversation, worktreesSupported, worktrees, target, onTargetChange, targetDisabled, disabled, isRunning,
  effectiveAgent, agentsLoaded, agents, tokensPerSecond, onAbort, tokenStats,
  estimatedCost, sessionTreeStats, contextTokens, effectiveModel, timeCreated,
  durationMs, visibleDurationMs,
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
          <TargetSelector directory={directory} worktreesSupported={!!worktreesSupported} worktrees={worktrees} target={target} onTargetChange={onTargetChange} disabled={targetDisabled} />
        )}
        {machineControl}
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
              <span className="oc-tps-hint" title="Estimated output tokens per model-request second, excluding tool and approval waits. Based on completed messages; includes request startup latency.">{formatTokensPerSecond(tokensPerSecond)} tok/s</span>
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
  selectorsLoading?: boolean;
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

export function ComposerToolbar({
  isBashMode, uiDisabled, disabled, disabledHint, effectiveAgent, agentsLoaded,
  agents, openAgentPicker, hasModels, modelUnavailable, openModelPicker,
  modelButtonLabel, effectiveModel, hasReasoning, openReasoningPicker,
  selectedReasoning, permissionControl, onLaunchRequest, launching, fileInputRef,
  addFiles, isDictationSupported, micRef, handleMicClick, micError, clearMicError,
  isRunning, onAbort, sending, submit, selectorsLoading,
}: ComposerToolbarProps) {
  return (
    <div className="oc-composer-bar">
      <div className="oc-composer-bar-left">
        {isBashMode ? (
          <span className="oc-bar-shell">shell</span>
        ) : selectorsLoading ? (
          <span className="oc-spinner" role="status" aria-label="Loading agents and models" />
        ) : (
          <>
            <button type="button" className="oc-bar-select" disabled={uiDisabled} onClick={openAgentPicker} title="Agent (click to change)">
              {effectiveAgent && agentsLoaded && <span className="oc-agent-swatch" aria-hidden="true" style={{ background: agentColor(effectiveAgent, agents) }} />}
              {effectiveAgent || 'Agent'}
            </button>
            {hasModels && (
              <button
                type="button"
                className={`oc-bar-select oc-bar-model${modelUnavailable ? ' oc-bar-select--warn' : ''}`}
                disabled={uiDisabled}
                onClick={openModelPicker}
                title={modelUnavailable ? 'Provider is not connected on this session\'s machine. Connect it or choose another provider.' : 'Model (click to change)'}
              >
                {modelUnavailable && <i className="bi bi-exclamation-triangle-fill" aria-hidden="true" />}
                {modelButtonLabel ? <ModelLabel model={effectiveModel}><span className="oc-bar-model-name">{modelButtonLabel}</span></ModelLabel> : 'Model'}
              </button>
            )}
            {hasReasoning && (
              <button type="button" className="oc-bar-select oc-bar-reasoning" disabled={uiDisabled} onClick={openReasoningPicker} title={`Reasoning level (${isMacPlatform() ? '⌥' : 'Alt'}+R to cycle)`}>
                {selectedReasoning || 'default'}
              </button>
            )}
            {permissionControl}
            {disabled && onLaunchRequest ? (
              <button type="button" className="oc-bar-launch" onClick={(event) => { event.stopPropagation(); onLaunchRequest(); }} disabled={launching} title={disabledHint || 'Launch the agent process'}>
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
        <input ref={fileInputRef} type="file" multiple style={{ display: 'none' }} onChange={(event) => {
          addFiles(Array.from(event.target.files || []));
          event.target.value = '';
        }} />
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
            data-perf="composer-send"
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
