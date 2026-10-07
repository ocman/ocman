/**
 * Auto-approve settings section and its prompt-section editor, split out
 * of SettingsSections to keep both files within the size budget.
 */
import { useState, useEffect } from 'react';
import { SaveStatus } from '../../components/SaveStatus';
import { SettingRow, SettingToggle, SettingNumber, SettingSelect } from '../../components/SettingRow';
import { useSaveStatus, useSettingSave } from '../../lib/useSaveStatus';
import { useUiStore } from '../../lib/uiStore';
import { useApiStore } from '../../lib/apiStore';
import { Button } from '../../components/Control';
import { ReviewerPromptSectionEditor, type PromptSection } from './ReviewerPromptSectionEditor';
import styles from './AutoApproveSection.module.css';

// ---------------------------------------------------------------------------
// Auto-approve (+ its prompt-section editor)
// ---------------------------------------------------------------------------

export function AutoApproveSection() {
  const autoApproveDefault = useUiStore((s) => s.autoApproveDefault);
  const setAutoApproveDefault = useUiStore((s) => s.setAutoApproveDefault);
  const autoApproveDelayMs = useUiStore((s) => s.autoApproveDelayMs);
  const setAutoApproveDelayMs = useUiStore((s) => s.setAutoApproveDelayMs);
  const promptSections = useUiStore((s) => s.promptSections);
  const setPromptSections = useUiStore((s) => s.setPromptSections);
  const setPromptSectionsApi = useApiStore((s) => s.setPromptSectionsApi);
  const setJudgeDelayApi = useApiStore((s) => s.setJudgeDelayApi);
  const getJudgeModel = useApiStore((s) => s.getJudgeModel);
  const setJudgeModelApi = useApiStore((s) => s.setJudgeModelApi);
  const getJudgeModelOptions = useApiStore((s) => s.getJudgeModelOptions);
  const delaySave = useSaveStatus();
  const sectionsSave = useSaveStatus();
  const autoApproveSave = useSettingSave();
  const modelSave = useSettingSave();
  const [judgeModel, setJudgeModel] = useState('');
  const [modelCatalog, setModelCatalog] = useState({ models: [] as string[], default: '' });

  useEffect(() => {
    const controller = new AbortController();
    // Best-effort: an older or remote backend may omit these fields, and a
    // missing catalog must degrade to "Default" rather than crash Settings.
    getJudgeModel().then((model) => setJudgeModel(model ?? '')).catch(() => { /* best-effort */ });
    getJudgeModelOptions(controller.signal)
      .then((catalog) => setModelCatalog({ models: catalog?.models ?? [], default: catalog?.default ?? '' }))
      .catch(() => { /* best-effort */ });
    return () => controller.abort();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  // "" is the stored value meaning "use the built-in default", so it is a
  // real option rather than an empty placeholder.
  const modelOptions = [
    { value: '', label: modelCatalog.default ? `Default (${modelCatalog.default})` : 'Default' },
    ...modelCatalog.models.map((value) => ({ value, label: value })),
  ];

  const saveSections = (next: PromptSection[]) => {
    setPromptSections(next);
    void sectionsSave.track(() => setPromptSectionsApi(next));
  };

  return (
    <>
      <SettingRow setting="auto-approve-default">
        <SettingToggle
          ariaLabel="Enable auto-approve by default"
          checked={autoApproveDefault}
          save={autoApproveSave}
          onSave={(next) => setAutoApproveDefault(next)}
        />
      </SettingRow>
      <SettingRow setting="human-review-window">
        <SettingNumber
          ariaLabel="Human review window in seconds"
          unit="s"
          min={0}
          max={60}
          value={Math.round(autoApproveDelayMs / 1000)}
          parse={(raw) => Math.max(0, Math.min(60, raw)) * 1000}
          save={delaySave}
          onSave={(ms) => {
            setAutoApproveDelayMs(ms);
            return setJudgeDelayApi(ms);
          }}
        />
      </SettingRow>
      <SettingRow setting="reviewer-model">
        <SettingSelect
          ariaLabel="Auto-approve reviewer model"
          placeholder="Default"
          searchLabel="Search models"
          value={judgeModel}
          options={modelOptions}
          save={modelSave}
          onSave={(next) => {
            setJudgeModel(next);
            return setJudgeModelApi(next);
          }}
        />
      </SettingRow>

      <SettingRow
        block
        setting="reviewer-prompt-sections"
        label={
          <>
            Reviewer prompt sections
            <SaveStatus state={sectionsSave.state} />
          </>
        }
      >
        <div className={styles.sections}>
          {promptSections.map((section, i) => (
            <ReviewerPromptSectionEditor
              key={i}
              section={section}
              number={i + 1}
              onChange={(updated) => {
                const next = [...promptSections];
                next[i] = updated;
                saveSections(next);
              }}
              onRemove={() => saveSections(promptSections.filter((_, j) => j !== i))}
            />
          ))}
          <Button
            type="button"
            variant="accent"
            className={styles.add}
            onClick={() => saveSections([...promptSections, { title: '', content: '' }])}
          >
            + Add section
          </Button>
        </div>
      </SettingRow>
    </>
  );
}
