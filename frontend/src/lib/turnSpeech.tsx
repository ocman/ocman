import { createContext, useCallback, useEffect, useMemo, useRef, useState } from 'react';
import type { Message, Part, SessionStatus } from './api';
import { createSpeechAnswers } from './speechAnswers';
import type { TurnStatsMap } from './turnStats';
import { useUiStore } from './uiStore';
export { finalAnswers } from './speechAnswers';
export { speechText } from './speechText';

export function useSpeechPlayback() {
  const supported = typeof window.speechSynthesis !== 'undefined' && typeof SpeechSynthesisUtterance !== 'undefined';
  const voiceURI = useUiStore((s) => s.speechVoiceURI);
  const rate = useUiStore((s) => s.speechRate);
  const [speakingId, setSpeakingId] = useState<string | null>(null);
  const [error, setError] = useState<string | null>(null);
  const current = useRef<SpeechSynthesisUtterance | null>(null);
  const stop = useCallback(() => {
    if (current.current) {
      current.current = null;
      window.speechSynthesis.cancel();
    }
    setSpeakingId(null);
  }, []);
  const play = useCallback((id: string, text: string) => {
    if (!supported || !text.trim()) return;
    stop();
    setError(null);
    const utterance = new SpeechSynthesisUtterance(text);
    const voices = window.speechSynthesis.getVoices();
    utterance.voice = voices.find((v) => v.voiceURI === voiceURI)
      ?? voices.find((v) => v.localService && v.lang.startsWith(navigator.language.split('-')[0]))
      ?? null;
    utterance.rate = rate;
    const finish = () => {
      if (current.current !== utterance) return;
      current.current = null;
      setSpeakingId(null);
    };
    utterance.onend = finish;
    utterance.onerror = (event) => {
      if (current.current !== utterance) return;
      finish();
      if (event.error !== 'canceled' && event.error !== 'interrupted') {
        setError('Speech playback failed. Try the speaker button or choose another voice in Settings.');
      }
    };
    current.current = utterance;
    setSpeakingId(id);
    try {
      window.speechSynthesis.speak(utterance);
    } catch {
      finish();
      setError('Speech playback is unavailable in this browser.');
    }
  }, [supported, stop, voiceURI, rate]);
  useEffect(() => stop, [stop]);
  return { supported, speakingId, error, play, stop };
}

export const TurnSpeechContext = createContext<{
  answers: Map<string, string>;
  speakingId: string | null;
  error: string | null;
  toggle: (id: string) => void;
}>({ answers: new Map(), speakingId: null, error: null, toggle: () => {} });

export function useTurnSpeech(messages: Message[], parts: Part[], turns: TurnStatsMap,
  sessionKey: string, status: SessionStatus | undefined, connected: boolean, reconciled: boolean) {
  const playback = useSpeechPlayback();
  const { play, stop } = playback;
  const autoRead = useUiStore((s) => s.autoReadAnswers);
  const latestUser = messages.findLast((m) => m.data.role === 'user')?.id;
  const latestAssistant = messages.findLast((m) => m.data.role === 'assistant')?.id;
  const selectAnswers = useMemo(() => createSpeechAnswers(), []);
  const answers = useMemo(() => {
    const excludedId = latestAssistant && (!reconciled || (status !== undefined && status !== 'done' && status !== 'waiting'))
      ? latestAssistant : undefined;
    return selectAnswers(messages, parts, turns, excludedId);
  }, [selectAnswers, messages, parts, turns, latestAssistant, reconciled, status]);
  const seen = useRef(new Set(messages.filter((m) => m.data.finish).map((m) => `${sessionKey}:${m.id}`)));
  const armed = useRef<string | null>(null);

  useEffect(() => {
    armed.current = null;
    return stop;
  }, [sessionKey, stop]);
  useEffect(() => { stop(); }, [latestUser, stop]);
  useEffect(() => {
    if (!autoRead || !connected) stop();
  }, [autoRead, connected, stop]);
  useEffect(() => {
    if (!autoRead || !connected) {
      armed.current = null;
      for (const id of answers.keys()) seen.current.add(`${sessionKey}:${id}`);
      return;
    }
    if (status === 'busy') {
      armed.current = latestUser ?? null;
      stop();
      return;
    }
    if (!reconciled) return;
    if (status !== 'done' && status !== 'waiting') {
      armed.current = null;
      return;
    }
    const text = latestAssistant && answers.get(latestAssistant);
    if (!text) return;
    const answerKey = `${sessionKey}:${latestAssistant}`;
    const shouldPlay = armed.current !== null && armed.current === latestUser && !seen.current.has(answerKey);
    armed.current = null;
    seen.current.add(answerKey);
    if (shouldPlay && document.visibilityState === 'visible' && document.hasFocus()) {
      play(latestAssistant, text);
    }
  }, [autoRead, connected, reconciled, status, latestUser, latestAssistant, answers, play, stop, sessionKey]);

  const toggle = useCallback((id: string) => {
    seen.current.add(`${sessionKey}:${id}`);
    if (playback.speakingId === id) stop();
    else {
      const text = answers.get(id);
      if (text) play(id, text);
    }
  }, [answers, playback.speakingId, play, stop, sessionKey]);
  return { answers: playback.supported ? answers : new Map<string, string>(), speakingId: playback.speakingId, error: playback.error, toggle };
}
