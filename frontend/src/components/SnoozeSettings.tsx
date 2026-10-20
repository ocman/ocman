import { Button } from './Control';
import { SettingRow, SettingNumber, SettingSelect, SettingText } from './SettingRow';
import { SaveStatus } from './SaveStatus';
import { useSettingSave } from '../lib/useSaveStatus';
import { useSnoozePreferences } from '../lib/snoozePreferences';
import { DEFAULT_SNOOZE_CHOICES, snoozeLabel, type SnoozeChoice } from '../lib/snooze';
import styles from './SnoozeSettings.module.css';

function ChoiceSetting({ choice, index }: { choice: SnoozeChoice; index: number }) {
  const choices = useSnoozePreferences((s) => s.choices);
  const setChoices = useSnoozePreferences((s) => s.setChoices);
  const kindSave = useSettingSave();
  const valueSave = useSettingSave();
  const removeSave = useSettingSave();
  const update = (next: SnoozeChoice) => setChoices(choices.map((item, i) => i === index ? next : item));
  return <SettingRow block label={`Quick choice ${index + 1}`} desc={snoozeLabel(choice)}>
    <div className={styles.controls}>
      <SettingSelect value={choice.kind} ariaLabel={`Quick choice ${index + 1} type`} placeholder="Choose type" searchLabel="Search snooze types"
        options={[{ value: 'duration', label: 'After a duration' }, { value: 'tomorrow', label: 'Tomorrow' }, { value: 'monday', label: 'Next Monday' }]}
        save={kindSave} onSave={(kind) => update(kind === 'duration' ? { kind, minutes: 60 } : { kind: kind as 'tomorrow' | 'monday', time: '08:00' })} />
      {choice.kind === 'duration'
        ? <SettingNumber value={choice.minutes} min={1} max={10080} unit="mins" ariaLabel={`Quick choice ${index + 1} minutes`} save={valueSave}
            onSave={(minutes) => update({ kind: 'duration', minutes: Math.max(1, Math.min(10080, Math.round(minutes))) })} />
        : <SettingText value={choice.time} type="time" ariaLabel={`Quick choice ${index + 1} time`} save={valueSave}
            onSave={(time) => { if (!/^([01]\d|2[0-3]):[0-5]\d$/.test(time)) throw new Error('Enter a valid time'); update({ ...choice, time }); }} />}
      <Button size="small" aria-label={`Remove quick choice ${index + 1}`} onClick={() => { void removeSave.track(async () => setChoices(choices.filter((_, i) => i !== index))); }}>Remove</Button>
      <SaveStatus state={removeSave.state} />
    </div>
  </SettingRow>;
}

export function SnoozeSettings() {
  const choices = useSnoozePreferences((s) => s.choices);
  const setChoices = useSnoozePreferences((s) => s.setChoices);
  const save = useSettingSave();
  return <SettingRow setting="snooze-quick-choices" block>
    <div className={styles.choices}>
      {choices.map((choice, index) => <ChoiceSetting key={index} choice={choice} index={index} />)}
    </div>
    <div className="oc-button-group">
      <Button size="small" onClick={() => { void save.track(async () => setChoices([...choices, { kind: 'duration', minutes: 60 }])); }}>Add quick choice</Button>
      <Button size="small" onClick={() => { void save.track(async () => setChoices(DEFAULT_SNOOZE_CHOICES)); }}>Reset defaults</Button>
      <SaveStatus state={save.state} />
    </div>
  </SettingRow>;
}
