/**
 * Auto-approve settings section and its prompt-section editor, split out
 * of SettingsSections to keep both files within the size budget.
 */
import { useState, useRef, useEffect } from 'react';
import { SaveStatus } from '../../components/SaveStatus';
import { SettingRow, SettingToggle, SettingNumber, SettingSelect } from '../../components/SettingRow';
import { useSaveStatus, useSettingSave } from '../../lib/useSaveStatus';
import { useUiStore } from '../../lib/uiStore';
import { useApiStore } from '../../lib/apiStore';

type PromptSection = { title: string; content: string; enabled?: boolean };
// ---------------------------------------------------------------------------
// Auto-approve (+ its prompt-section editor)
// ---------------------------------------------------------------------------

function PromptSectionEditor({
  section,
  onChange,
  onRemove,
}: {
  section: PromptSection;
  onChange: (s: PromptSection) => void;
  onRemove: () => void;
}) {
  // Track textarea height so it grows with content.
  const textareaRef = useRef<HTMLTextAreaElement>(null);
  // Missing `enabled` (legacy rows) is treated as enabled.
  const enabled = section.enabled !== false;
  return (
    <div className="settings-prompt-section">
      <div className="settings-prompt-section-header">
        <label className="settings-prompt-section-toggle">
          <input
            type="checkbox"
            checked={enabled}
            aria-label="Enable rule"
            onChange={(e) => onChange({ ...section, enabled: e.target.checked })}
          />
          <span aria-hidden="true" />
        </label>
        <input
          type="text"
          className="settings-prompt-section-title"
          placeholder="Section title"
          value={section.title}
          onChange={(e) => onChange({ ...section, title: e.target.value })}
        />
        <button
          type="button"
          className="settings-prompt-section-remove"
          aria-label="Remove section"
          onClick={onRemove}
        >
          &#x2715;
        </button>
      </div>
      <textarea
        ref={textareaRef}
        className="settings-prompt-section-content"
        placeholder="Describe the rule in plain language. The AI reviewer will follow this as an additional instruction."
        value={section.content}
        rows={3}
        onChange={(e) => {
          onChange({ ...section, content: e.target.value });
          // Auto-grow: reset height first so shrinking works too.
          if (textareaRef.current) {
            textareaRef.current.style.height = 'auto';
            textareaRef.current.style.height = `${textareaRef.current.scrollHeight}px`;
          }
        }}
      />
    </div>
  );
}

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
      <SettingRow
        label="Enable by default"
        desc="Automatically start the AI permission reviewer for every new session. You can also enable or disable it per session from the permission prompt."
      >
        <SettingToggle
          ariaLabel="Enable auto-approve by default"
          checked={autoApproveDefault}
          save={autoApproveSave}
          onSave={(next) => setAutoApproveDefault(next)}
        />
      </SettingRow>
      <SettingRow
        label="Human review window"
        desc="How long to wait after a permission prompt appears before the AI reviewer starts. Gives you time to approve or reject manually."
      >
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
      <SettingRow
        label="Reviewer model"
        desc="The model that judges permission prompts. A fast, cheap model is usually the right pick."
      >
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
        label={
          <>
            Reviewer prompt sections
            <SaveStatus state={sectionsSave.state} />
          </>
        }
        desc={<>Extra rules appended to the AI reviewer&apos;s prompt. Each section
          appears as a named block the model reads before deciding. Use this
          to allow or deny specific patterns your team knows are safe.</>}
      >
        <div className="settings-prompt-sections">
          {promptSections.map((section, i) => (
            <PromptSectionEditor
              key={i}
              section={section}
              onChange={(updated) => {
                const next = [...promptSections];
                next[i] = updated;
                saveSections(next);
              }}
              onRemove={() => saveSections(promptSections.filter((_, j) => j !== i))}
            />
          ))}
          <button
            type="button"
            className="settings-prompt-add"
            onClick={() => saveSections([...promptSections, { title: '', content: '' }])}
          >
            + Add section
          </button>
        </div>
      </SettingRow>
    </>
  );
}
