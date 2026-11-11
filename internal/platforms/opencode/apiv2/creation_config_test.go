package ocv2

import (
	"encoding/json"
	"net/http"
	"testing"
)

func TestCreateConfigurationPreservesEmptyRulesAndDefaultVariant(t *testing.T) {
	f := fakeServer(t)
	f.json("POST /api/session", http.StatusOK, fakeSessionJSON)
	status, body := cmpDo(t, http.MethodPost, f.URL+"/session", nil,
		`{"agent":"plan","model":{"providerID":"openai","modelID":"gpt","variant":"default"},"permission":[]}`)
	if status != http.StatusOK {
		t.Fatalf("create: %d %s", status, body)
	}
	cmpJSON(t, "native create", f.one("POST /api/session").Body,
		`{"agent":"plan","model":{"providerID":"openai","id":"gpt"},"permissions":[]}`)
	var receipt map[string]any
	if err := json.Unmarshal(body, &receipt); err != nil || receipt["configurationApplied"] != true {
		t.Fatalf("empty permission rules not acknowledged: %v %v", receipt, err)
	}
}
