import { TextField, TextareaField } from '../../components/Control';
import { IconButton } from '../../components/IconButton';
import { ToggleField } from '../../components/ToggleField';
import styles from './ReviewerPromptSectionEditor.module.css';

export type PromptSection = { title: string; content: string; enabled?: boolean };

export function ReviewerPromptSectionEditor({ section, number, onChange, onRemove }: {
  section: PromptSection;
  number: number;
  onChange: (section: PromptSection) => void;
  onRemove: () => void;
}) {
  return <section className={styles.root} aria-label={`Prompt section ${number}`}>
    <div className={styles.header}>
      <ToggleField checked={section.enabled !== false} label={`Enable section ${number}`} onChange={(e) => onChange({ ...section, enabled: e.target.checked })} />
      <TextField className={styles.title} aria-label={`Section ${number} title`} placeholder="Section title" value={section.title} onChange={(e) => onChange({ ...section, title: e.target.value })} />
      <IconButton label={`Remove section ${number}`} icon="bi-trash" variant="danger" onClick={onRemove} />
    </div>
    <TextareaField className={styles.content} aria-label={`Section ${number} instructions`}
      placeholder="Describe the rule in plain language. The AI reviewer will follow this as an additional instruction."
      value={section.content} rows={3} onChange={(e) => {
        onChange({ ...section, content: e.target.value });
        const textarea = e.currentTarget;
        textarea.style.height = 'auto';
        textarea.style.height = `${textarea.scrollHeight + textarea.offsetHeight - textarea.clientHeight}px`;
      }} />
  </section>;
}
