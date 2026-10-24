package platforms

import (
	"encoding/json"
	"reflect"
	"testing"
)

func TestLivePromptFields(t *testing.T) {
	prompt := LivePrompt{"id": "per_1", "count": 2, "patterns": []any{"a", 3, "b"}, "always": []string{"c"}, "empty": []any{}}
	if prompt.StringField("id") != "per_1" || prompt.StringField("count") != "" || prompt.StringField("missing") != "" {
		t.Fatal("string field read a non-string value")
	}
	for key, want := range map[string][]string{"patterns": {"a", "b"}, "always": {"c"}, "empty": {}, "id": nil, "missing": nil} {
		if got := prompt.StringsField(key); !reflect.DeepEqual(got, want) {
			t.Fatalf("StringsField(%q) = %#v, want %#v", key, got, want)
		}
	}
}

func TestSessionModelAvailabilityJSON(t *testing.T) {
	for _, available := range []bool{false, true} {
		body, err := json.Marshal(SessionModel{IsAvailable: available})
		if err != nil {
			t.Fatal(err)
		}
		var decoded struct {
			IsAvailable *bool `json:"isAvailable"`
		}
		if err := json.Unmarshal(body, &decoded); err != nil {
			t.Fatal(err)
		}
		if decoded.IsAvailable == nil || *decoded.IsAvailable != available {
			t.Fatalf("availability %v must be explicit in JSON, got %s", available, body)
		}
	}
}
