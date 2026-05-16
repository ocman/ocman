/**
 * useComposerAudio — recording start/stop, transcription, and dictation state.
 *
 * Two recording paths, tried in order:
 *
 *  1. Web Speech API (SpeechRecognition / webkitSpeechRecognition)
 *     Natively supported in Safari/iOS/iPad and Chrome. Provides streaming
 *     interim results so text appears while the user is still speaking.
 *     No server-side whisper required.
 *
 *  2. Whisper fallback (MediaRecorder → WAV → /api/transcribe)
 *     Used when the Speech API is unavailable (Firefox, etc.) and the
 *     server-side whisper binary is present. Records everything then
 *     transcribes on stop.
 *
 * Extracted from Composer.tsx per issue #14.
 */
import { useState, useRef, useCallback, useEffect } from 'react';
import { encodeWav } from '../../lib/audio/encodeWav';
import { useApiStore } from '../../lib/apiStore';

// ---------------------------------------------------------------------------
// Web Speech API types (not yet in TypeScript's lib.dom.d.ts)
// ---------------------------------------------------------------------------

interface SpeechRecognition extends EventTarget {
  continuous: boolean;
  interimResults: boolean;
  lang: string;
  onresult: ((ev: SpeechRecognitionEvent) => void) | null;
  onerror: ((ev: SpeechRecognitionErrorEvent) => void) | null;
  onend: (() => void) | null;
  start(): void;
  stop(): void;
  abort(): void;
}

// ---------------------------------------------------------------------------
// Types
// ---------------------------------------------------------------------------

interface WhisperCtx {
  kind: 'whisper';
  stream: MediaStream;
  audioCtx: AudioContext;
  processor: ScriptProcessorNode;
  chunks: Float32Array[];
}

interface SpeechCtx {
  kind: 'speech';
  recognition: SpeechRecognition;
  /**
   * Number of interim characters currently appended at the tail of the
   * textarea. On each onresult event we strip exactly this many chars off the
   * end, then re-append the latest interim text. This means we never touch
   * anything the user typed before or during the session.
   */
  interimLen: number;
}

type RecordingCtx = WhisperCtx | SpeechCtx;

export interface ComposerAudioControls {
  isRecording: boolean;
  micError: string | null;
  setMicError: (err: string | null) => void;
  micRef: React.RefObject<HTMLButtonElement | null>;
  handleMicClick: () => Promise<void>;
  /** True when at least one recording path is available */
  isDictationSupported: boolean;
}

// ---------------------------------------------------------------------------
// Web Speech API feature detection
// ---------------------------------------------------------------------------

function getSpeechRecognitionCtor(): (new () => SpeechRecognition) | null {
  if (typeof window === 'undefined') return null;
  // Both the Speech API and getUserMedia require a secure context
  // (HTTPS or localhost). On insecure HTTP origins Safari/Chrome define
  // the constructor but refuse to start it, so we gate on isSecureContext.
  if (!window.isSecureContext) return null;
  // Standard and webkit-prefixed variants
  const w = window as unknown as {
    SpeechRecognition?: new () => SpeechRecognition;
    webkitSpeechRecognition?: new () => SpeechRecognition;
  };
  return w.SpeechRecognition ?? w.webkitSpeechRecognition ?? null;
}

function isSpeechApiAvailable(): boolean {
  return getSpeechRecognitionCtor() !== null;
}

// ---------------------------------------------------------------------------
// Hook
// ---------------------------------------------------------------------------

export function useComposerAudio({
  whisperAvailable,
  disabled,
  inputRef,
}: {
  whisperAvailable: boolean | undefined;
  disabled: boolean | undefined;
  inputRef: React.RefObject<HTMLTextAreaElement | null>;
}): ComposerAudioControls {
  const [isRecording, setIsRecording] = useState(false);
  const [micError, setMicError] = useState<string | null>(null);
  const micRef = useRef<HTMLButtonElement | null>(null);
  const recordingRef = useRef<RecordingCtx | null>(null);
  const disabledRef = useRef(disabled);
  useEffect(() => { disabledRef.current = disabled; }, [disabled]);

  const transcribe = useApiStore((state) => state.transcribe);

  // -------------------------------------------------------------------------
  // Button visual state
  // -------------------------------------------------------------------------

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

  // -------------------------------------------------------------------------
  // Append text to the composer input
  // -------------------------------------------------------------------------

  const appendText = useCallback((text: string) => {
    const el = inputRef.current;
    if (!el || !text) return;
    el.value = (el.value ? el.value + ' ' : '') + text;
    el.dispatchEvent(new Event('input'));
    el.focus();
  }, [inputRef]);

  // -------------------------------------------------------------------------
  // Whisper path: stop recording and upload
  // -------------------------------------------------------------------------

  const stopWhisperRecording = useCallback((): Blob | null => {
    const ctx = recordingRef.current;
    if (!ctx || ctx.kind !== 'whisper') return null;
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

  const submitWhisperRecording = useCallback(async () => {
    if (!recordingRef.current || recordingRef.current.kind !== 'whisper') return;
    setMicState('transcribing');
    const blob = stopWhisperRecording();
    if (blob && blob.size > 44) {
      try {
        const text = await transcribe(blob);
        appendText(text);
      } catch (err) {
        console.error('Transcription failed', err);
      }
    }
    setMicState('idle');
  }, [setMicState, stopWhisperRecording, transcribe, appendText]);

  const cancelWhisperRecording = useCallback(() => {
    const ctx = recordingRef.current;
    if (!ctx || ctx.kind !== 'whisper') return;
    recordingRef.current = null;
    ctx.processor.disconnect();
    ctx.stream.getTracks().forEach(t => t.stop());
    ctx.audioCtx.close();
    setMicState('idle');
  }, [setMicState]);

  // -------------------------------------------------------------------------
  // Speech API path: stop recognition
  // -------------------------------------------------------------------------

  const stopSpeechRecognition = useCallback(() => {
    const ctx = recordingRef.current;
    if (!ctx || ctx.kind !== 'speech') return;
    recordingRef.current = null;
    try { ctx.recognition.stop(); } catch { /* ignore */ }
    setMicState('idle');
  }, [setMicState]);

  // -------------------------------------------------------------------------
  // Unified stop / cancel
  // -------------------------------------------------------------------------

  const stopRecording = useCallback(async () => {
    const ctx = recordingRef.current;
    if (!ctx) return;
    if (ctx.kind === 'speech') {
      stopSpeechRecognition();
    } else {
      await submitWhisperRecording();
    }
  }, [stopSpeechRecognition, submitWhisperRecording]);

  const cancelRecording = useCallback(() => {
    const ctx = recordingRef.current;
    if (!ctx) return;
    if (ctx.kind === 'speech') {
      stopSpeechRecognition();
    } else {
      cancelWhisperRecording();
    }
  }, [stopSpeechRecognition, cancelWhisperRecording]);

  // -------------------------------------------------------------------------
  // Start recording: Speech API first, Whisper as fallback
  // -------------------------------------------------------------------------

  const startSpeechRecognition = useCallback(() => {
    const Ctor = getSpeechRecognitionCtor()!;
    const recognition = new Ctor();
    recognition.continuous = true;
    recognition.interimResults = true;
    recognition.lang = ''; // use browser/OS default language

    const ctx: SpeechCtx = { kind: 'speech', recognition, interimLen: 0 };
    recordingRef.current = ctx;

    recognition.onresult = (event: SpeechRecognitionEvent) => {
      const el = inputRef.current;
      if (!el) return;

      let newConfirmed = '';
      let interim = '';
      for (let i = event.resultIndex; i < event.results.length; i++) {
        const result = event.results[i];
        if (result.isFinal) {
          newConfirmed += result[0].transcript;
        } else {
          interim += result[0].transcript;
        }
      }

      // Strip the interim tail we appended last time, then build the new tail.
      // This way we never rewrite text the user typed themselves.
      const current = el.value;
      const base = current.slice(0, current.length - ctx.interimLen);

      // Helper: append text to a string, inserting a space separator if needed.
      const join = (a: string, b: string) =>
        b ? (a && !a.endsWith(' ') ? a + ' ' + b : a + b) : a;

      // Build the confirmed portion (permanently appended to base).
      const confirmed = join(base, newConfirmed.trimStart());

      // Build the interim preview tail (will be stripped next event).
      const withInterim = join(confirmed, interim.trimStart());

      // Track exactly how many chars the interim tail is so we can strip it.
      ctx.interimLen = withInterim.length - confirmed.length;

      if (el.value !== withInterim) {
        el.value = withInterim;
        el.dispatchEvent(new Event('input'));
      }
    };

    recognition.onerror = (event: SpeechRecognitionErrorEvent) => {
      // 'no-speech' and 'aborted' are not real errors
      if (event.error === 'no-speech' || event.error === 'aborted') return;
      console.error('SpeechRecognition error', event.error);
      setMicError('Dictation error: ' + event.error);
      recordingRef.current = null;
      setMicState('idle');
    };

    recognition.onend = () => {
      // onend fires both on manual stop and on natural silence timeout.
      // If we still hold a reference, the recognition ended by itself —
      // clean up and go idle.
      if (recordingRef.current?.kind === 'speech') {
        recordingRef.current = null;
        setMicState('idle');
      }
    };

    recognition.start();
    setMicState('recording');
  }, [inputRef, setMicState, setMicError]);

  const startWhisperRecording = useCallback(async () => {
    if (!navigator.mediaDevices?.getUserMedia) {
      setMicError('Dictation is not supported in this browser. Please use a modern browser like Chrome or Edge.');
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

    recordingRef.current = { kind: 'whisper', stream, audioCtx, processor, chunks };
    setMicState('recording');
  }, [setMicState, setMicError]);

  // -------------------------------------------------------------------------
  // Mic button handler
  // -------------------------------------------------------------------------

  const handleMicClick = useCallback(async () => {
    if (disabledRef.current) return;

    if (recordingRef.current) {
      await stopRecording();
      return;
    }

    setMicError(null);
    try {
      if (isSpeechApiAvailable()) {
        startSpeechRecognition();
      } else if (whisperAvailable) {
        await startWhisperRecording();
      } else {
        setMicError('Dictation is not supported in this browser.');
      }
    } catch (err) {
      console.error('Microphone access failed', err);
      setMicError('Microphone access denied. Please allow microphone access in your browser settings to use dictation.');
      setMicState('idle');
    }
  }, [setMicState, setMicError, stopRecording, startSpeechRecognition, startWhisperRecording, whisperAvailable]);

  // -------------------------------------------------------------------------
  // Keyboard shortcuts while recording
  // -------------------------------------------------------------------------

  useEffect(() => {
    if (!isRecording) return;
    const onKeyDown = (e: KeyboardEvent) => {
      if (e.code === 'Escape') {
        e.preventDefault();
        cancelRecording();
      }
      // Do NOT intercept other keys — the user should be able to type
      // alongside dictation when using the Speech API.
    };
    window.addEventListener('keydown', onKeyDown, true);
    return () => window.removeEventListener('keydown', onKeyDown, true);
  }, [isRecording, cancelRecording]);

  // -------------------------------------------------------------------------
  // isDictationSupported: true when at least one path is available
  // -------------------------------------------------------------------------

  const isDictationSupported =
    isSpeechApiAvailable() || (!!whisperAvailable && !!navigator.mediaDevices?.getUserMedia);

  return {
    isRecording,
    micError,
    setMicError,
    micRef,
    handleMicClick,
    isDictationSupported,
  };
}
