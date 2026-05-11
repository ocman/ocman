/**
 * useComposerAudio — recording start/stop, ScriptProcessorNode wiring,
 * WAV encoding, transcription call, micError state and inline error display.
 *
 * Extracted from Composer.tsx per issue #14.
 */
import { useState, useRef, useCallback, useEffect } from 'react';
import { encodeWav } from '../../lib/audio/encodeWav';
import { useApiStore } from '../../lib/apiStore';

interface RecordingCtx {
  stream: MediaStream;
  audioCtx: AudioContext;
  processor: ScriptProcessorNode;
  chunks: Float32Array[];
}

export interface ComposerAudioControls {
  isRecording: boolean;
  micError: string | null;
  setMicError: (err: string | null) => void;
  micRef: React.RefObject<HTMLButtonElement | null>;
  handleMicClick: () => Promise<void>;
  isDictationSupported: boolean;
}

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
  }, [setMicState, stopRecording, transcribe, inputRef]);

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
        setMicError('Dictation is not supported in this browser. Please use a modern browser like Chrome or Edge.');
        return;
      }
      setMicError(null);
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
      setMicError('Microphone access denied. Please allow microphone access in your browser settings to use dictation.');
      setMicState('idle');
    }
  }, [setMicState, submitRecording]);

  // While recording, any key submits (except Escape which cancels).
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

  const isDictationSupported = !!whisperAvailable && !!navigator.mediaDevices?.getUserMedia;

  return {
    isRecording,
    micError,
    setMicError,
    micRef,
    handleMicClick,
    isDictationSupported,
  };
}
