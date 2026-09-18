import { createContext, useCallback, useEffect, useMemo, useRef, useState } from 'react';
import { renderToStaticMarkup } from 'react-dom/server';
import Markdown from 'react-markdown';
import remarkGfm from 'remark-gfm';
import type { Message, Part, SessionStatus } from './api';
import { parsePart } from './convertMessages';
import type { TurnStatsMap } from './turnStats';
import { useUiStore } from './uiStore';

/** Read original text only. Rendered thread text also contains reasoning and errors. */
export function finalAnswers(messages: Message[], parts: Part[], turns: TurnStatsMap): Map<string, string> {
  const answers = new Map<string, string>();
  for (const message of messages) {
    const turn = turns.get(message.id);
    if (!turn?.isSummaryAnchor || turn.isLive || message.data.role !== 'assistant'
      || message.data.error || message.data.summary || message.data.agent === 'compaction'
      || !['stop', 'end_turn'].includes(message.data.finish ?? '')) continue;
    const text = parts.filter((part) => part.messageId === message.id).map(parsePart)
      .filter((part) => part.type === 'text' && !part.synthetic && !part.ignored)
      .map((part) => part.text ?? '').join('\n\n').trim();
    const prose = speechText(text);
    if (prose) answers.set(message.id, prose);
  }
  return answers;
}

export function speechText(markdown: string): string {
  // Reuse the installed Markdown parser: regex stripping can leak fenced commands.
  const html = renderToStaticMarkup(<Markdown remarkPlugins={[remarkGfm]} components={{
    pre: () => null,
    img: () => null,
    p: ({ children }) => <p>{children}{'\n'}</p>,
    li: ({ children }) => <li>{children}{'\n'}</li>,
    tr: ({ children }) => <tr>{children}{'\n'}</tr>,
    td: ({ children }) => <td>{children}{' '}</td>,
    th: ({ children }) => <th>{children}{' '}</th>,
    h1: ({ children }) => <h1>{children}{'\n'}</h1>,
    h2: ({ children }) => <h2>{children}{'\n'}</h2>,
    h3: ({ children }) => <h3>{children}{'\n'}</h3>,
    h4: ({ children }) => <h4>{children}{'\n'}</h4>,
    h5: ({ children }) => <h5>{children}{'\n'}</h5>,
    h6: ({ children }) => <h6>{children}{'\n'}</h6>,
  }}>{markdown}</Markdown>);
  return new DOMParser().parseFromString(html, 'text/html').body.textContent?.replace(/\s+/g, ' ').trim() ?? '';
}

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
  const answers = useMemo(() => {
    const result = finalAnswers(messages, parts, turns);
    if (latestAssistant && (!reconciled || (status !== undefined && status !== 'done' && status !== 'waiting'))) {
      result.delete(latestAssistant);
    }
    return result;
  }, [messages, parts, turns, latestAssistant, reconciled, status]);
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
