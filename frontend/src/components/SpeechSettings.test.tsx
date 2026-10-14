// @vitest-environment jsdom
import { act, cleanup, screen, fireEvent } from '@testing-library/react';
import { renderWithQuery } from '../test/renderWithQuery';
import { afterEach, beforeEach, expect, it, vi } from 'vitest';
import { SpeechSettings } from './SpeechSettings';
import { useUiStore } from '../lib/uiStore';
import { api } from '../lib/api';

beforeEach(() => { vi.spyOn(api, 'whisperStatus').mockResolvedValue({ available: false }); });

afterEach(() => { cleanup(); vi.unstubAllGlobals(); vi.restoreAllMocks(); });

it('shows missing Whisper in speech settings', async () => {
  renderWithQuery(<SpeechSettings />);
  expect(await screen.findByText(/Whisper is unavailable/)).toBeInTheDocument();
  expect(screen.getByText(/Install whisper-cpp and a model/)).toBeInTheDocument();
});

it('reports available Whisper', async () => {
  vi.mocked(api.whisperStatus).mockResolvedValue({ available: true });
  renderWithQuery(<SpeechSettings />);
  expect(await screen.findByText('Whisper is available.')).toBeInTheDocument();
});

it('reports a failed Whisper availability check', async () => {
  vi.mocked(api.whisperStatus).mockRejectedValue(new Error('offline'));
  renderWithQuery(<SpeechSettings />);
  expect(await screen.findByText('Could not check Whisper availability.')).toBeInTheDocument();
});
it('disables automatic reading when speech is unavailable', () => {
  vi.stubGlobal('speechSynthesis', undefined);
  renderWithQuery(<SpeechSettings />);
  expect(screen.getByRole('checkbox', { name: 'Automatically read answers aloud' })).toBeDisabled();
  expect(screen.queryByRole('combobox')).not.toBeInTheDocument();
});
it('loads delayed voices, persists preferences and previews the selected voice', async () => {
  useUiStore.setState({ autoReadAnswers: false, speechVoiceURI: '', speechRate: 1 });
  const synth = new EventTarget();
  const voice = { name: 'Voice', lang: 'en-US', voiceURI: 'voice', localService: true };
  const getVoices = vi.fn(() => [] as typeof voice[]);
  const speak = vi.fn();
  const cancel = vi.fn();
  vi.stubGlobal('speechSynthesis', Object.assign(synth, { getVoices, speak, cancel }));
  vi.stubGlobal('SpeechSynthesisUtterance', class {
    text: string;
    constructor(text: string) { this.text = text; }
  });
  const { unmount } = renderWithQuery(<SpeechSettings />);
  getVoices.mockReturnValue([voice, { ...voice, voiceURI: 'remote', name: 'Remote', localService: false }]);
  act(() => synth.dispatchEvent(new Event('voiceschanged')));
  expect(screen.getByRole('option', { name: 'Voice (en-US, local)' })).toBeInTheDocument();
  expect(screen.getByRole('option', { name: 'Remote (en-US, online)' })).toBeInTheDocument();
  fireEvent.click(screen.getByRole('checkbox', { name: 'Automatically read answers aloud' }));
  fireEvent.change(screen.getByRole('combobox'), { target: { value: 'voice' } });
  fireEvent.change(screen.getByRole('spinbutton'), { target: { value: '1.5' } });
  expect(useUiStore.getState()).toMatchObject({ autoReadAnswers: true, speechVoiceURI: 'voice', speechRate: 1.5 });
  fireEvent.click(screen.getByRole('button', { name: 'Preview voice' }));
  expect(speak.mock.calls[0][0]).toMatchObject({ voice, rate: 1.5 });
  fireEvent.click(screen.getByRole('button', { name: 'Stop preview' }));
  expect(cancel).toHaveBeenCalled();
  fireEvent.click(screen.getByRole('button', { name: 'Preview voice' }));
  act(() => speak.mock.calls[1][0].onerror({ error: 'not-allowed' }));
  expect(screen.getByText(/Speech playback failed/)).toBeInTheDocument();
  // Let the existing setting-save feedback finish before unmounting.
  await act(() => new Promise((resolve) => setTimeout(resolve, 350)));
  unmount();
});
