import { useState, useEffect, useRef, useCallback, useMemo, useImperativeHandle } from 'react';
import './Composer.css';
import { useComposerDrafts } from './useComposerDrafts';
import { useShortcut } from '../../lib/shortcutRegistry';
import { BackendUnavailableError, type SlashCommand } from '../../lib/api';
import { useComposerAttachments } from './useComposerAttachments';
import { useSlashMenu } from './useSlashMenu';
import { useComposerPickers } from './useComposerPickers';
import { useRunningDuration } from './useRunningDuration';
import { describeModel } from './composerModel';
import { agentColor } from '../../lib/agentColor';
import { ComposerFooter, ComposerToolbar } from './ComposerChrome';
import { ComposerDialogs } from './ComposerDialogs';
import { SlashCommandMenu } from './SlashCommandMenu';
import { QueuedMessages } from './QueuedMessages';
import { useComposerAudio } from './useComposerAudio';
import { routeComposerSubmit } from './composerSubmit';
import { KNOWN_AGENTS, modelHasVariants } from '../../lib/commands/builtinCommands';
import { ModalReturnFocusContext } from '../ModalReturnFocusContext';
import type { ComposerProps } from './composerTypes';
import { ComposerMachineSelector } from './ComposerMachineSelector';

export type { AttachedImage } from './useComposerAttachments';
export type { ComposerHandle } from './composerTypes';

export function Composer({
  onSend, onRetryChange, onCommand, onShell, shellExec, queuedShellCommand,
  onCancelQueuedShell, queuedMessages, onRemoveQueuedMessage, onMoveQueuedMessage,
  onAbort, isRunning, disabled, whisperAvailable, models, modelEntries,
  selectedModel, onModelChange, onToggleFavorite, onRefreshModels, activeAgent,
  selectedAgent, onAgentChange, agents, agentsLoaded, commands, contextTokens,
  activeDurationMs, timeCreated, durationMs, sessionId, platform, draftKey: draftKeyProp, tokensPerSecond,
  tokenStats, estimatedCost, sessionTreeStats, selectedReasoning, onReasoningChange,
  disabledHint, onLaunchRequest, launching, directory, newConversation,
  worktreesSupported, worktrees, permissionControl, composerRef, target, onTargetChange,
  remoteId, onMachineChange,
}: ComposerProps) {
  const wrapRef = useRef<HTMLDivElement>(null);
  const inputRef = useRef<HTMLTextAreaElement>(null);
  const fileInputRef = useRef<HTMLInputElement>(null);
  const [sending, setSending] = useState(false);
  const [switchingMachine, setSwitchingMachine] = useState(false);
  const sendingRef = useRef(false);
  const mountedRef = useRef(true);
  const sessionIdRef = useRef(sessionId);
  // Drafts are keyed separately from the session: a new conversation keeps
  // one shared draft while it has no session to attach it to.
  const draftKey = draftKeyProp ?? sessionId;
  const draftKeyRef = useRef(draftKey);
  const { clearDraftNow, scheduleDraftSave } = useComposerDrafts(inputRef, draftKey, draftKeyRef);
  const visibleDurationMs = useRunningDuration(activeDurationMs, isRunning);
  const attachments = useComposerAttachments(sessionIdRef, disabled || sending || switchingMachine, platform);
  const { images, files } = attachments;

  useEffect(() => { sessionIdRef.current = sessionId; }, [sessionId]);
  useEffect(() => { draftKeyRef.current = draftKey; }, [draftKey]);
  useEffect(() => { sendingRef.current = sending; }, [sending]);
  useEffect(() => {
    mountedRef.current = true;
    return () => { mountedRef.current = false; };
  }, []);

  // Restore focus after disabled controls commit, without stealing it from another control.
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
  const hasAgents = !!(agents && agents.length > 0);
  const cyclableAgents = hasAgents
    ? (agents || []).filter((a) => a.mode !== 'subagent' && !a.hidden).map((a) => a.name)
    : KNOWN_AGENTS;
  const agentOptions = Array.from(new Set([activeAgent, ...cyclableAgents].filter((a): a is string => !!a)));
  const effectiveAgent = selectedAgent || activeAgent || '';
  const hasVariants = modelHasVariants(selectedModel, modelEntries);
  const slash = useSlashMenu(sessionId, { hasModels, hasAgents, activeAgent, hasVariants }, commands);
  const pickers = useComposerPickers({
    inputRef, sessionIdRef: draftKeyRef, scheduleDraftSave, models, agents, agentOptions,
    onModelChange, onAgentChange, onRefreshModels,
  });
  const { openModelPicker, openAgentPicker, openSkillPicker, openRoutinePicker } = pickers;
  useImperativeHandle(composerRef, () => ({ openModelPicker, openAgentPicker }), [openModelPicker, openAgentPicker]);

  const clearComposerInput = useCallback(() => {
    const el = inputRef.current;
    if (!el) return;
    el.value = '';
    slash.close();
    const key = draftKeyRef.current;
    if (key) clearDraftNow(key);
  }, [clearDraftNow, slash]);

  const selectSlashCommand = useCallback((cmd: SlashCommand) => {
    const el = inputRef.current;
    if (!el) return;
    if (cmd.name === 'model') { clearComposerInput(); openModelPicker(''); return; }
    if (cmd.name === 'agent' || cmd.name === 'agents') { clearComposerInput(); openAgentPicker(''); return; }
    if (cmd.name === 'help') { clearComposerInput(); pickers.help.setOpen(true); return; }
    if (cmd.name === 'skills') { clearComposerInput(); openSkillPicker(''); return; }
    if (cmd.name === 'routines') { clearComposerInput(); openRoutinePicker(''); return; }
    if (cmd.name === 'variants') { clearComposerInput(); pickers.reasoning.setOpen(true); return; }
    el.value = '/' + cmd.name + ' ';
    el.focus();
    slash.close();
  }, [clearComposerInput, openModelPicker, openAgentPicker, openSkillPicker, openRoutinePicker, pickers, slash]);

  // Switching machines captures the draft once, so no new dictation may start mid-switch.
  const audioDisabled = disabled || switchingMachine;
  const { isRecording, isBusy: audioBusy, micError, setMicError, micRef, handleMicClick, isDictationSupported } = useComposerAudio({ whisperAvailable, disabled: audioDisabled, inputRef });
  const clearAfterSubmit = () => {
    if (inputRef.current) inputRef.current.value = '';
    slash.close();
    setIsBashMode(false);
    attachments.clear();
    const key = draftKeyRef.current;
    if (key) clearDraftNow(key);
  };

  const runSubmit = async (execute: () => void | Promise<void>, retryBackend = false) => {
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
        const submitted = execute();
        if (submitted) await submitted;
        if (mountedRef.current) clearAfterSubmit();
        break;
      } catch (err) {
        if (!retryBackend || !(err instanceof BackendUnavailableError) || retries >= MAX_BACKEND_RETRIES) break;
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
      void runSubmit(() => onCommand(route.command, route.args));
    } else if (route.kind === 'shell' && onShell) {
      void runSubmit(() => onShell(route.command));
    } else {
      const text = route.kind === 'send' ? route.text : route.kind === 'noop' ? '' : raw.trim();
      void runSubmit(() => onSend?.(withFileReferences(text), images.length > 0 ? images : undefined, queue,
        attachments.deferredFiles.length > 0 ? attachments.deferredFiles : undefined), true);
    }
  };

  const handleInputKeyDown = (e: React.KeyboardEvent<HTMLTextAreaElement>) => {
    if (disabled || sendingRef.current) return;
    const el = e.currentTarget;
    const hasArg = el.value.includes(' ');
    const menuDown = e.key === 'ArrowDown' || (e.key === 'Tab' && !e.shiftKey);
    const menuUp = e.key === 'ArrowUp' || (e.key === 'Tab' && e.shiftKey);
    if (slash.open && !hasArg && (menuDown || menuUp)) {
      e.preventDefault(); slash.moveIndex(menuDown ? 1 : -1); return;
    }
    if (slash.open && e.key === 'Escape') { e.preventDefault(); slash.close(); return; }
    if (slash.open && !hasArg && e.key === 'Enter' && !e.shiftKey) {
      const cmd = slash.filtered[slash.index];
      if (cmd) { e.preventDefault(); selectSlashCommand(cmd); }
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
      e.preventDefault(); clearAfterSubmit(); return;
    }
    if (e.key === 'Enter' && !e.shiftKey) { e.preventDefault(); submit(e.ctrlKey || e.metaKey); }
  };

  const handleInput = (e: React.FormEvent<HTMLTextAreaElement>) => {
    const el = e.currentTarget;
    setIsBashMode(el.value.startsWith('!') && !!shellExec);
    slash.syncToInput(el.value);
    const key = draftKeyRef.current;
    if (key) scheduleDraftSave(key, () => el.value);
  };
  useEffect(() => {
    const handleKeyDown = (e: KeyboardEvent) => {
      if (e.defaultPrevented) return;
      if (e.key === 'Escape' && isRunning && onAbort) { e.preventDefault(); onAbort(); }
    };
    window.addEventListener('keydown', handleKeyDown);
    return () => window.removeEventListener('keydown', handleKeyDown);
  }, [isRunning, onAbort]);

  const effectiveModel = selectedModel || '';
  const { label: modelButtonLabel, unavailable: modelUnavailable, reasoningOptions } = describeModel(effectiveModel, modelEntries);
  const hasReasoning = reasoningOptions.length > 0;
  const dictationShortcut = useMemo(() => ({
    id: 'composer.dictation', scope: 'composer' as const, keys: { code: 'KeyD', alt: true },
    description: 'Start dictation (voice input)',
    enabled: () => !!(isDictationSupported && !audioBusy && !audioDisabled),
    handler: () => { void handleMicClick(); },
  }), [isDictationSupported, audioBusy, audioDisabled, handleMicClick]);
  const reasoningCycleShortcut = useMemo(() => ({
    id: 'composer.reasoning-cycle', scope: 'composer' as const, keys: { code: 'KeyR', alt: true },
    description: 'Cycle reasoning level',
    enabled: () => !!(reasoningOptions.length > 0 && !disabled),
    handler: () => {
      if (reasoningOptions.length === 0 || !onReasoningChange) return;
      const cycle = ['', ...reasoningOptions];
      const cur = cycle.indexOf(selectedReasoning ?? '');
      onReasoningChange(cycle[(cur + 1) % cycle.length]);
    },
  }), [reasoningOptions, disabled, onReasoningChange, selectedReasoning]);
  useShortcut(dictationShortcut);
  useShortcut(reasoningCycleShortcut);

  const uiDisabled = disabled || sending || switchingMachine || attachments.pending > 0;
  const shellQueueID = '__shell__';
  const queueItems = [...(queuedMessages || []), ...(queuedShellCommand ? [{
    id: shellQueueID, text: `!${queuedShellCommand}`, hasImages: false,
    canMove: false, removeLabel: 'Cancel queued shell command',
  }] : [])];

  return (
    <ModalReturnFocusContext value={inputRef}>
      <div className={`oc-composer-wrap${uiDisabled ? ' oc-composer-disabled' : ''}`} ref={wrapRef} data-perf="composer"
        onDragOver={attachments.handleDragOver} onDrop={attachments.handleDrop}
        onClick={disabled && onLaunchRequest ? onLaunchRequest : undefined}
        style={disabled && onLaunchRequest ? { cursor: 'pointer' } : undefined}>
        <ComposerDialogs pickers={pickers} models={models} modelEntries={modelEntries}
          effectiveModel={effectiveModel} onModelChange={onModelChange} onToggleFavorite={onToggleFavorite}
          agentOptions={agentOptions} agents={agents} activeAgent={activeAgent} effectiveAgent={effectiveAgent}
          onAgentChange={onAgentChange} commands={slash.commands} reasoningOptions={reasoningOptions}
          selectedReasoning={selectedReasoning} onReasoningChange={onReasoningChange} />
        {slash.open && <SlashCommandMenu commands={slash.filtered} activeIndex={slash.index} menuRef={slash.menuRef}
          listboxId={slash.listboxId} optionId={slash.optionId} onSelect={selectSlashCommand} onHover={slash.setIndex} />}
        {isRecording && <div className="oc-recording-banner">
          <div className="oc-recording-pulse" /><span className="oc-recording-label">Listening</span>
          <span className="oc-recording-hint">Esc to cancel</span>
          <button type="button" className="oc-recording-stop" onClick={() => void handleMicClick()} aria-label="Stop recording">Stop</button>
        </div>}
        {!disabled && <QueuedMessages messages={queueItems}
          onRemove={(id) => id === shellQueueID ? onCancelQueuedShell?.() : onRemoveQueuedMessage?.(id)} onMove={onMoveQueuedMessage} />}
        <div className="oc-composer" style={isBashMode ? { borderLeftColor: '#f38ba8' }
          : !disabled && effectiveAgent && agentsLoaded ? { borderLeftColor: agentColor(effectiveAgent, agents) } : undefined}>
          {(images.length > 0 || files.length > 0) && <div className="oc-composer-images">
            {images.map((img, i) => <div key={i} className="oc-composer-image-thumb">
              <img src={img.url} alt={`Attachment ${i + 1}`} />
              <button type="button" className="oc-composer-image-remove" title={`Remove image attachment ${i + 1}`}
                aria-label={`Remove image attachment ${i + 1}`} onClick={() => attachments.removeImage(i)}>{'\u00D7'}</button>
            </div>)}
            {files.map((file, i) => <div key={`${file.path || file.name}:${i}`} className="oc-composer-file-thumb" title={file.path || file.name}>
              <span className="oc-composer-file-icon">file</span><span className="oc-composer-file-name">{file.name}</span>
              <button type="button" className="oc-composer-image-remove" title={`Remove attached file ${file.name}, attachment ${i + 1}`}
                aria-label={`Remove attached file ${file.name}, attachment ${i + 1}`} onClick={() => attachments.removeFile(i)}>{'\u00D7'}</button>
            </div>)}
          </div>}
          <textarea ref={inputRef} className="oc-composer-input" rows={1} disabled={uiDisabled}
            placeholder={disabled ? (disabledHint || 'No live connection to the agent') : undefined}
            autoComplete="off" autoCorrect="off" autoCapitalize="off" spellCheck={false} {...slash.inputAria}
            onBlur={slash.close} onKeyDown={handleInputKeyDown} onInput={handleInput} onPaste={attachments.handlePaste}
            data-1p-ignore data-lpignore="true" data-bwignore data-form-type="other" />
          <ComposerToolbar isBashMode={isBashMode} uiDisabled={uiDisabled} disabled={disabled} disabledHint={disabledHint}
            effectiveAgent={effectiveAgent} agentsLoaded={agentsLoaded} agents={agents}
            openAgentPicker={() => { if (!uiDisabled) openAgentPicker(); }} hasModels={hasModels} modelUnavailable={modelUnavailable}
            openModelPicker={() => { if (!uiDisabled) openModelPicker(); }} modelButtonLabel={modelButtonLabel} effectiveModel={effectiveModel}
            hasReasoning={hasReasoning} openReasoningPicker={() => { if (!uiDisabled) pickers.reasoning.setOpen(true); }}
            selectedReasoning={selectedReasoning} permissionControl={permissionControl} onLaunchRequest={onLaunchRequest}
            launching={launching} fileInputRef={fileInputRef} addFiles={(selectedFiles) => { void attachments.addFiles(selectedFiles); }}
            isDictationSupported={isDictationSupported} micRef={micRef} handleMicClick={() => { void handleMicClick(); }}
            micError={micError} clearMicError={() => setMicError(null)} isRunning={isRunning} onAbort={onAbort} sending={sending} submit={submit} />
        </div>
        <ComposerFooter directory={directory} newConversation={newConversation} worktreesSupported={worktreesSupported} worktrees={worktrees}
          machineControl={newConversation && directory && onMachineChange && (
            <ComposerMachineSelector
              key={`${sessionId}:${directory}:${remoteId}`}
              directory={directory}
              remoteId={remoteId}
              disabled={sending || switchingMachine || audioBusy || attachments.pending > 0 || images.length > 0 || files.length > 0}
              onSelect={async (machine) => {
                setSwitchingMachine(true);
                try {
                  await onMachineChange(machine);
                } finally {
                  setSwitchingMachine(false);
                }
              }}
            />
          )}
          sessionId={sessionId} disabled={disabled} targetDisabled={uiDisabled} target={target} onTargetChange={onTargetChange} isRunning={isRunning} effectiveAgent={effectiveAgent} agentsLoaded={agentsLoaded}
          agents={agents} tokensPerSecond={tokensPerSecond} onAbort={onAbort} tokenStats={tokenStats} estimatedCost={estimatedCost}
          sessionTreeStats={sessionTreeStats} contextTokens={contextTokens} effectiveModel={effectiveModel}
          timeCreated={timeCreated} durationMs={durationMs} visibleDurationMs={visibleDurationMs} />
      </div>
    </ModalReturnFocusContext>
  );
}
