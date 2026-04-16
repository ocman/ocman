import { useState, useEffect, useRef, useCallback, memo } from 'react';
import './Composer.css';
import { getDraft, saveDraft, clearDraft } from '../../lib/composerDraft';
import { useApiStore } from '../../lib/apiStore';
import { api, type SlashCommand } from '../../lib/api';

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

type SelectGroup = { label?: string; options: { value: string; label: string }[] };

function BarSelect({ groups, value, disabled, onChange, title, placeholder }: {
  groups: SelectGroup[];
  value: string;
  disabled?: boolean;
  onChange: (value: string) => void;
  title?: string;
  placeholder?: string;
}) {
  const [open, setOpen] = useState(false);
  const ref = useRef<HTMLDivElement>(null);

  useEffect(() => {
    if (!open) return;
    function onClickOutside(e: MouseEvent) {
      if (ref.current && !ref.current.contains(e.target as Node)) setOpen(false);
    }
    document.addEventListener('mousedown', onClickOutside);
    return () => document.removeEventListener('mousedown', onClickOutside);
  }, [open]);

  return (
    <div className="oc-model-select" ref={ref}>
      <button
        type="button"
        className="oc-bar-select"
        disabled={disabled}
        onClick={() => !disabled && setOpen((o) => !o)}
        title={title}
      >
        {value || placeholder || title || ''}
      </button>
      {open && (
        <div className="oc-model-dropdown">
          {groups.map((group, gi) => (
            <div key={group.label ?? gi} className="oc-model-group">
              {group.label && <div className="oc-model-group-label">{group.label}</div>}
              {group.options.map((opt) => (
                <button
                  key={opt.value}
                  type="button"
                  className={`oc-model-option${opt.value === value ? ' active' : ''}`}
                  onMouseDown={(e) => {
                    e.preventDefault();
                    onChange(opt.value);
                    setOpen(false);
                  }}
                >
                  {opt.label}
                </button>
              ))}
            </div>
          ))}
        </div>
      )}
    </div>
  );
}

const KNOWN_AGENTS = ['build', 'developer', 'plan', 'architect', 'ba', 'brainstormer', 'reviewer', 'security'];

const BUILTIN_COMMANDS: SlashCommand[] = [
  { name: 'compact', description: 'Summarize conversation history to free up context window' },
  { name: 'new', description: 'Start a new session in the same project directory' },
];

function ComposerImpl({
  onSend,
  onCommand,
  onAbort,
  isRunning,
  disabled,
  whisperAvailable,
  models,
  activeModel,
  selectedModel,
  onModelChange,
  activeAgent,
  selectedAgent,
  onAgentChange,
  contextTokens,
  sessionId,
  directory,
  tokensPerSecond,
}: {
  onSend?: (text: string, images?: AttachedImage[]) => void;
  onCommand?: (command: string, args: string) => void;
  onAbort?: () => void;
  isRunning: boolean;
  disabled?: boolean;
  whisperAvailable?: boolean;
  models?: string[];
  activeModel?: string;
  selectedModel?: string;
  onModelChange?: (model: string) => void;
  activeAgent?: string;
  selectedAgent?: string;
  onAgentChange?: (agent: string) => void;
  contextTokens?: number;
  sessionId?: string;
  directory?: string;
  tokensPerSecond?: number;
}) {
  const wrapRef = useRef<HTMLDivElement>(null);
  const inputRef = useRef<HTMLTextAreaElement>(null);
  const micRef = useRef<HTMLButtonElement>(null);
  const fileInputRef = useRef<HTMLInputElement>(null);
  const onSendRef = useRef(onSend);
  const isRunningRef = useRef(isRunning);
  const disabledRef = useRef(disabled);
  const mountedRef = useRef(false);
  const recordingRef = useRef<RecordingCtx | null>(null);
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

  const onCommandRef = useRef(onCommand);
  useEffect(() => { onCommandRef.current = onCommand; }, [onCommand]);
  const [slashCommands, setSlashCommands] = useState<SlashCommand[]>([]);
  const [showSlashMenu, setShowSlashMenu] = useState(false);
  const [slashFilter, setSlashFilter] = useState('');
  const [slashIndex, setSlashIndex] = useState(0);
  const slashMenuRef = useRef<HTMLDivElement>(null);
  const showSlashMenuRef = useRef(false);
  const slashIndexRef = useRef(0);
  const filteredCommandsRef = useRef<SlashCommand[]>([]);

  useEffect(() => {
    if (!directory) return;
    let cancelled = false;
    api.commands(directory).then(cmds => {
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
  }, [directory]);

  const filteredCommands = slashCommands.filter(cmd =>
    cmd.name.toLowerCase().startsWith(slashFilter.toLowerCase())
  );

  useEffect(() => { showSlashMenuRef.current = showSlashMenu; }, [showSlashMenu]);
  useEffect(() => { slashIndexRef.current = slashIndex; }, [slashIndex]);
  useEffect(() => { filteredCommandsRef.current = filteredCommands; }, [filteredCommands]);

  useEffect(() => {
    if (!showSlashMenu || !slashMenuRef.current) return;
    const active = slashMenuRef.current.querySelector('.oc-slash-item.active');
    if (active) active.scrollIntoView({ block: 'nearest' });
  }, [slashIndex, showSlashMenu]);

  const selectSlashCommand = useCallback((cmd: SlashCommand) => {
    const el = inputRef.current;
    if (!el) return;
    el.value = '/' + cmd.name + ' ';
    el.style.height = 'auto';
    el.style.height = Math.min(el.scrollHeight, 200) + 'px';
    el.focus();
    setShowSlashMenu(false);
    setSlashFilter('');
    setSlashIndex(0);
  }, []);

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
    const handleClick = (e: MouseEvent) => {
      const target = e.target as HTMLElement;
      if (!inputRef.current || inputRef.current.disabled) return;
      if (target.closest('button, a, select, input, textarea, [role="button"], [contenteditable], pre, code, .oc-cmd-palette')) return;
      inputRef.current.focus();
    };
    document.addEventListener('click', handleClick);
    return () => document.removeEventListener('click', handleClick);
  }, []);

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

  const handleMicClick = useCallback(async () => {
    if (disabledRef.current) return;

    if (recordingRef.current) {
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
  }, [setMicState, stopRecording, transcribe]);

  useEffect(() => {
    if (mountedRef.current) return;
    mountedRef.current = true;
    const el = inputRef.current;
    if (!el) return;

    const handleInputKeyDown = (e: KeyboardEvent) => {
      if (disabledRef.current) return;

      if (showSlashMenuRef.current) {
        const cmds = filteredCommandsRef.current;
        if (e.key === 'ArrowDown') {
          e.preventDefault();
          const next = (slashIndexRef.current + 1) % Math.max(cmds.length, 1);
          slashIndexRef.current = next;
          el.dispatchEvent(new CustomEvent('oc-slash-nav', { detail: next }));
          return;
        }
        if (e.key === 'ArrowUp') {
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
        if (e.key === 'Tab' || (e.key === 'Enter' && !e.shiftKey)) {
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

      if (e.key === 'Enter' && !e.shiftKey) {
        e.preventDefault();
        const trimmed = el.value.trim();
        const imgs = imagesRef.current;
        if (!trimmed && imgs.length === 0) return;

        if (trimmed.startsWith('/') && onCommandRef.current) {
          const spaceIdx = trimmed.indexOf(' ');
          const command = spaceIdx > 0 ? trimmed.slice(1, spaceIdx) : trimmed.slice(1);
          const args = spaceIdx > 0 ? trimmed.slice(spaceIdx + 1).trim() : '';
          onCommandRef.current(command, args);
        } else {
          onSendRef.current?.(trimmed, imgs.length > 0 ? imgs : undefined);
        }

        el.value = '';
        el.style.height = 'auto';
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
      if (val.startsWith('/') && !val.includes('\n')) {
        const filter = val.slice(1).split(' ')[0] || '';
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
    el.addEventListener('oc-clear-images', handleClearImages);
    el.addEventListener('oc-paste-images', handlePasteImages);
    el.addEventListener('oc-slash-update', handleSlashUpdate);
    el.addEventListener('oc-slash-nav', handleSlashNav);
    el.addEventListener('oc-slash-close', handleSlashClose);
    el.addEventListener('oc-slash-select', handleSlashSelect);
    return () => {
      el.removeEventListener('oc-clear-images', handleClearImages);
      el.removeEventListener('oc-paste-images', handlePasteImages);
      el.removeEventListener('oc-slash-update', handleSlashUpdate);
      el.removeEventListener('oc-slash-nav', handleSlashNav);
      el.removeEventListener('oc-slash-close', handleSlashClose);
      el.removeEventListener('oc-slash-select', handleSlashSelect);
    };
  }, [addImageFiles, selectSlashCommand]);

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

  const agentOptions = Array.from(new Set([activeAgent, ...KNOWN_AGENTS].filter((a): a is string => !!a)));
  const effectiveAgent = selectedAgent || activeAgent || '';
  const effectiveModel = selectedModel || activeModel || '';
  const modelGroups = (models || []).reduce<Array<{ label: string; options: { value: string; label: string }[] }>>((groups, model) => {
    const slashIndex = model.indexOf('/');
    const provider = slashIndex > 0 ? model.slice(0, slashIndex) : 'Other';
    const optionLabel = slashIndex > 0 ? model.slice(slashIndex + 1) : model;
    const existing = groups.find((group) => group.label === provider);

    if (existing) {
      existing.options.push({ value: model, label: optionLabel });
      return groups;
    }

    groups.push({
      label: provider,
      options: [{ value: model, label: optionLabel }],
    });
    return groups;
  }, []);

  return (
    <div
      className={`oc-composer-wrap${disabled ? ' oc-composer-disabled' : ''}`}
      ref={wrapRef}
      onDragOver={handleDragOver}
      onDrop={handleDrop}
    >
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
      <div className="oc-composer">
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
          placeholder={disabled ? 'No running OpenCode instance' : undefined}
        />
        <div className="oc-composer-bar">
          <div className="oc-composer-bar-left">
            <BarSelect
              groups={[{ options: agentOptions.map((a) => ({ value: a, label: a })) }]}
              value={effectiveAgent}
              disabled={disabled}
              onChange={(v) => onAgentChange?.(v)}
              title="Agent"
            />
            {models && models.length > 0 && (
              <BarSelect
                groups={modelGroups}
                value={models.includes(effectiveModel) ? effectiveModel : ''}
                disabled={disabled}
                onChange={(v) => onModelChange?.(v)}
                title="Model"
              />
            )}
            {disabled && <span className="oc-bar-hint">No running OpenCode instance</span>}
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
              <span className="oc-bar-dots">
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
              <span className={`oc-context-usage${pct != null && pct > 80 ? ' oc-context-warn' : ''}`} title={contextWindow ? `${contextTokens.toLocaleString()} / ${contextWindow.toLocaleString()} tokens` : `${contextTokens.toLocaleString()} tokens used`}>
                {formatTokenCount(contextTokens)}{pct != null && ` (${pct.toFixed(0)}%)`}
              </span>
            );
          })()}
          <span className="oc-keybind-hint">? for shortcuts</span>
        </span>
      </div>
    </div>
  );
}

export const Composer = memo(ComposerImpl, (prev, next) =>
  prev.isRunning === next.isRunning &&
  prev.disabled === next.disabled &&
  prev.whisperAvailable === next.whisperAvailable &&
  prev.activeModel === next.activeModel &&
  prev.selectedModel === next.selectedModel &&
  prev.activeAgent === next.activeAgent &&
  prev.selectedAgent === next.selectedAgent &&
  prev.contextTokens === next.contextTokens &&
  prev.sessionId === next.sessionId &&
  prev.directory === next.directory &&
  prev.tokensPerSecond === next.tokensPerSecond &&
  (prev.models?.length || 0) === (next.models?.length || 0) &&
  (prev.models || []).every((model, i) => model === (next.models || [])[i])
);
