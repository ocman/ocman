import { useEffect, useState } from 'react';
import { useUiStore } from '../lib/uiStore';
import { useSpeechPlayback } from '../lib/turnSpeech';
import { useSettingSave } from '../lib/useSaveStatus';
import { SettingNumber, SettingRow, SettingToggle } from './SettingRow';
import { SaveStatus } from './SaveStatus';

export function SpeechSettings() {
  const autoRead = useUiStore((s) => s.autoReadAnswers);
  const setAutoRead = useUiStore((s) => s.setAutoReadAnswers);
  const voiceURI = useUiStore((s) => s.speechVoiceURI);
  const setVoiceURI = useUiStore((s) => s.setSpeechVoiceURI);
  const rate = useUiStore((s) => s.speechRate);
  const setRate = useUiStore((s) => s.setSpeechRate);
  const autoSave = useSettingSave();
  const voiceSave = useSettingSave();
  const rateSave = useSettingSave();
  const { supported, play, stop, speakingId, error } = useSpeechPlayback();
  const [voices, setVoices] = useState<SpeechSynthesisVoice[]>([]);
  useEffect(() => {
    if (!supported) return;
    const update = () => setVoices(window.speechSynthesis.getVoices());
    update();
    window.speechSynthesis.addEventListener('voiceschanged', update);
    return () => window.speechSynthesis.removeEventListener('voiceschanged', update);
  }, [supported]);

  return <>
    <SettingRow label="Read answers aloud" desc={supported
      ? 'Automatically read new final answers in the focused session tab. Off by default. Code blocks, reasoning, and tool output are skipped. Saved for this browser.'
      : 'Speech playback is unavailable in this browser.'}>
      <SettingToggle ariaLabel="Automatically read answers aloud" checked={autoRead} disabled={!supported}
        save={autoSave} onSave={setAutoRead} />
    </SettingRow>
    {supported && <>
      <SettingRow label="Reading voice" desc="Local voices stay on your device. Online voices may send answer text to the voice service.">
        <select aria-label="Reading voice" value={voiceURI} onChange={(event) => {
          const value = event.target.value;
          stop();
          void voiceSave.track(async () => setVoiceURI(value)).catch(() => {});
        }}>
          <option value="">Automatic, prefer local</option>
          {voices.map((voice) => <option key={voice.voiceURI} value={voice.voiceURI}>
            {voice.name} ({voice.lang}, {voice.localService ? 'local' : 'online'})
          </option>)}
        </select>
        <SaveStatus state={voiceSave.state} />
      </SettingRow>
      <SettingRow label="Reading speed">
        <SettingNumber ariaLabel="Reading speed" value={rate} unit="×" min={0.5} max={2} step={0.1}
          save={rateSave} onSave={setRate} />
        <button type="button" onClick={() => speakingId ? stop() : play('preview', 'This is how your final answers will sound.')}>
          {speakingId ? 'Stop preview' : 'Preview voice'}
        </button>
      </SettingRow>
      {error && <p role="status">{error}</p>}
    </>}
  </>;
}
