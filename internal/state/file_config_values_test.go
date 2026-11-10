package state

import "testing"

func TestFileSettingValueConvertsOnlyStructuredSettings(t *testing.T) {
	const object = `{"enabled":true}`
	for _, key := range []string{"auto_archive", "model_fallthrough", "notifications.badge", "link_preview_rules", "project:/repo", "project-defaults:repo"} {
		if _, ok := fileSettingValue(key, object).(map[string]any); !ok {
			t.Errorf("structured setting %s lost its YAML object", key)
		}
	}
	if _, ok := fileSettingValue("prompt_sections", `[{"title":"rule"}]`).([]any); !ok {
		t.Error("prompt sections lost their YAML sequence")
	}
	for _, key := range []string{"pr_prompt_template", "issue_prompt_template", "review_prompt_template", "session.default_agent"} {
		if value, ok := fileSettingValue(key, object).(string); !ok || value != object {
			t.Errorf("text setting %s was interpreted as JSON", key)
		}
	}
	for _, value := range []string{"{invalid}", "12", "null", "false"} {
		if actual, ok := fileSettingValue("auto_archive", value).(string); !ok || actual != value {
			t.Errorf("changed a non-object setting value %q", value)
		}
	}
}
