package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
)

func TestLinkPreviewRules(t *testing.T) {
	s := &Server{stateDB: openTestStateDB(t)}
	request := func(method, body string) *httptest.ResponseRecorder {
		t.Helper()
		r := httptest.NewRecorder()
		s.handleLinkPreviewRules(r, httptest.NewRequest(method, "/api/settings/link-preview-rules", strings.NewReader(body)))
		return r
	}
	if got := request(http.MethodGet, ""); got.Code != 200 || strings.TrimSpace(got.Body.String()) != `{"rules":[]}` {
		t.Fatalf("empty GET: %d %s", got.Code, got.Body.String())
	}
	valid := `{"rules":[{"pattern":"ABC-\\d+","replacement":"https://tracker.example.com/issues/$&"}]}`
	var want any
	if err := json.Unmarshal([]byte(valid), &want); err != nil {
		t.Fatal(err)
	}
	checkStored := func() {
		t.Helper()
		got := request(http.MethodGet, "")
		var actual any
		if err := json.Unmarshal(got.Body.Bytes(), &actual); err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(actual, want) {
			t.Fatalf("stored GET: %d %s", got.Code, got.Body.String())
		}
	}
	if got := request(http.MethodPost, valid); got.Code != 200 {
		t.Fatalf("save: %d %s", got.Code, got.Body.String())
	}
	checkStored()
	for _, body := range []string{
		`{"rules":[{"pattern":"[","replacement":"https://tracker.example.com/$&"}]}`,
		`{"rules":[{"pattern":"ABC-.*","replacement":"javascript:alert(1)"}]}`,
		`{"rules":[{"pattern":"","replacement":"https://tracker.example.com/$&"}]}`,
		`{"rules":[{"pattern":"^","replacement":"https://tracker.example.com/$&"}]}`,
		`{"rules":[{"pattern":"ABC-\\d+","replacement":"https://tracker.example.com/$1"}]}`,
		`{"rules":[{"pattern":"ABC-\\d+","replacement":"https://tracker.example.com/"}]}`,
		`{"rules":null}`,
	} {
		if got := request(http.MethodPost, body); got.Code != http.StatusBadRequest {
			t.Errorf("invalid %s: %d %s", body, got.Code, got.Body.String())
		}
	}
	checkStored()
	if got := request(http.MethodPost, `{"rules":[{"pattern":"ABC-(\\d+)","replacement":"https://tracker.example.com/$1"}]}`); got.Code != http.StatusOK {
		t.Fatalf("capture group save: %d %s", got.Code, got.Body.String())
	}
	if got := request(http.MethodPost, `{"rules":[]}`); got.Code != http.StatusOK {
		t.Fatalf("remove all rules: %d %s", got.Code, got.Body.String())
	}
}
