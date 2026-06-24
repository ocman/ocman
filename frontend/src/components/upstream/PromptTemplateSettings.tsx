import { useEffect, useState } from 'react';
import {
  fetchPromptTemplates,
  savePromptTemplates,
  type PromptTemplates,
} from '../../lib/upstreamApi';
import { SaveStatus } from '../SaveStatus';
import { useSaveStatus } from '../../lib/useSaveStatus';

// Defaults match the Go constants in internal/server/handlers_prompt_templates.go.
// Kept in sync manually — these strings are *user-facing defaults*, so a
// change here is a UX choice, not a backend change. The backend's
// fallback applies independently if the user resets without explicit
// "set to default".
const DEFAULT_PR_TEMPLATE = `Please handle PR #{number}: {title}

Author: {author}
Link: {url}
Branch: {branch}

Description:
{body}`;

const DEFAULT_ISSUE_TEMPLATE = `Please handle issue #{number}: {title}

Author: {author}
Link: {url}

Description:
{body}`;

/**
 * PromptTemplateSettings is the settings-page editor for the two
 * prompt templates used by the PR/Issue sidebar's launch action.
 *
 * Saves on blur (so the user doesn't have to hit a submit button)
 * with a debounced visual confirmation. Reset buttons restore the
 * built-in defaults baked into ocman.
 */
export function PromptTemplateSettings() {
  const [templates, setTemplates] = useState<PromptTemplates>({
    pr: DEFAULT_PR_TEMPLATE,
    issue: DEFAULT_ISSUE_TEMPLATE,
  });
  const [loaded, setLoaded] = useState(false);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    let cancelled = false;
    fetchPromptTemplates()
      .then((t) => {
        if (cancelled) return;
        setTemplates(t);
        setLoaded(true);
      })
      .catch((err: unknown) => {
        if (cancelled) return;
        setError(err instanceof Error ? err.message : String(err));
        setLoaded(true);
      });
    return () => {
      cancelled = true;
    };
  }, []);

  // Returns the save promise so the calling editor can track its own status.
  const save = async (next: Partial<PromptTemplates>) => {
    setError(null);
    try {
      const updated = await savePromptTemplates(next);
      setTemplates(updated);
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err));
      throw err;
    }
  };

  if (!loaded) {
    return (
      <div className="settings-prompt-templates">
        <em>Loading…</em>
      </div>
    );
  }

  return (
    <div className="settings-prompt-templates" data-testid="prompt-templates-settings">
      <TemplateEditor
        label="PR template"
        value={templates.pr}
        onSave={(v) => save({ pr: v })}
        onReset={() => save({ pr: DEFAULT_PR_TEMPLATE })}
        placeholdersNote="Placeholders: {number} {title} {body} {url} {branch} {author} {host} {repo}"
        testId="pr-template"
      />
      <TemplateEditor
        label="Issue template"
        value={templates.issue}
        onSave={(v) => save({ issue: v })}
        onReset={() => save({ issue: DEFAULT_ISSUE_TEMPLATE })}
        placeholdersNote="Placeholders: {number} {title} {body} {url} {author} {host} {repo} (no {branch} for issues)"
        testId="issue-template"
      />
      {error && <div className="settings-prompt-templates-error">{error}</div>}
    </div>
  );
}

interface TemplateEditorProps {
  label: string;
  value: string;
  onSave: (next: string) => Promise<void>;
  onReset: () => Promise<void>;
  placeholdersNote: string;
  testId: string;
}

function TemplateEditor({ label, value, onSave, onReset, placeholdersNote, testId }: TemplateEditorProps) {
  const [draft, setDraft] = useState(value);
  const { state, track } = useSaveStatus();
  // Sync when the parent's persisted value changes (e.g. after Reset).
  useEffect(() => {
    setDraft(value);
  }, [value]);

  return (
    <div className="settings-prompt-template" data-testid={`prompt-template-${testId}`}>
      <div className="settings-prompt-template-header">
        <label>{label}</label>
        <SaveStatus state={state} />
        <button type="button" onClick={() => void track(() => onReset())} data-testid={`prompt-template-${testId}-reset`}>
          Reset to default
        </button>
      </div>
      <textarea
        value={draft}
        onChange={(e) => setDraft(e.target.value)}
        onBlur={() => {
          if (draft !== value) void track(() => onSave(draft));
        }}
        rows={10}
        spellCheck={false}
      />
      <div className="settings-prompt-template-note">{placeholdersNote}</div>
    </div>
  );
}
