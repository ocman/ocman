import { useEffect, useState } from 'react';
import { useUiStore } from '../lib/uiStore';
import { useSpeechPlayback } from '../lib/turnSpeech';
import { useSettingSave } from '../lib/useSaveStatus';
import { SettingNumber, SettingRow, SettingToggle } from './SettingRow';
import { SaveStatus } from './SaveStatus';
import { Button, SelectField } from './Control';
import { useWhisperStatus } from '../lib/settingsQueries';

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
  const whisperQ = useWhisperStatus();
  const whisper = whisperQ.isError ? 'Could not check Whisper availability.'
    : !whisperQ.data ? 'Checking Whisper availability…'
      : whisperQ.data.available ? 'Whisper is available.'
        : 'Whisper is unavailable. Local voice input is disabled. Install whisper-cpp and a model to enable it.';
  useEffect(() => {
    if (!supported) return;
    const update = () => setVoices(window.speechSynthesis.getVoices());
    update();
    window.speechSynthesis.addEventListener('voiceschanged', update);
    return () => window.speechSynthesis.removeEventListener('voiceschanged', update);
  }, [supported]);

  return <>
    <SettingRow setting="local-voice-input" desc={whisper}>{null}</SettingRow>
    <SettingRow setting="read-aloud" desc={supported ? undefined : 'Speech playback is unavailable in this browser.'}>
      <SettingToggle ariaLabel="Automatically read answers aloud" checked={autoRead} disabled={!supported}
        save={autoSave} onSave={setAutoRead} />
    </SettingRow>
    {supported && <>
      <SettingRow setting="reading-voice">
        <SelectField aria-label="Reading voice" value={voiceURI} onChange={(event) => {
          const value = event.target.value;
          stop();
          void voiceSave.track(async () => setVoiceURI(value)).catch(() => {});
        }}>
          <option value="">Automatic, prefer local</option>
          {voices.map((voice) => <option key={voice.voiceURI} value={voice.voiceURI}>
            {voice.name} ({voice.lang}, {voice.localService ? 'local' : 'online'})
          </option>)}
        </SelectField>
        <SaveStatus state={voiceSave.state} />
      </SettingRow>
      <SettingRow setting="reading-speed">
        <SettingNumber ariaLabel="Reading speed" value={rate} unit="×" min={0.5} max={2} step={0.1}
          save={rateSave} onSave={setRate} />
        <Button type="button" size="small" onClick={() => speakingId ? stop() : play('preview', 'This is how your final answers will sound.')}>
          {speakingId ? 'Stop preview' : 'Preview voice'}
        </Button>
      </SettingRow>
      {error && <p role="status">{error}</p>}
    </>}
  </>;
}
