package notifications

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestBadgeSettingsDefaultsAndPersistence(t *testing.T) {
	m := fixture(t)
	defaults := BadgeSettings{Sessions: true, Primary: true, Factory: true}
	settings, err := m.BadgeSettings(t.Context())
	if err != nil || settings != defaults {
		t.Fatalf("defaults = %+v, %v", settings, err)
	}
	settings, err = New(Deps{}).BadgeSettings(t.Context())
	if err != nil || settings != defaults {
		t.Fatalf("no-store defaults = %+v, %v", settings, err)
	}
	for _, tc := range []struct {
		method, body string
		want         BadgeSettings
	}{
		{http.MethodGet, "", defaults},
		{http.MethodPost, `{"sessions":false,"primary":true,"factory":false,"routines":true}`, BadgeSettings{Primary: true, Routines: true}},
		{http.MethodGet, "", BadgeSettings{Primary: true, Routines: true}},
		{http.MethodPost, `{"primary":false}`, BadgeSettings{Routines: true}},
	} {
		r := httptest.NewRequest(tc.method, "http://ocman.test/api/settings/notification-badge", strings.NewReader(tc.body))
		r.Header.Set("Origin", "http://ocman.test")
		w := httptest.NewRecorder()
		m.badgeSettings(w, r)
		var got BadgeSettings
		if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil || w.Code != 200 || got != tc.want {
			t.Fatalf("%s response = %d %s, %v; want %+v", tc.method, w.Code, w.Body.String(), err, tc.want)
		}
	}
	settings, err = New(Deps{Store: m.d.Store}).BadgeSettings(t.Context())
	if err != nil || settings != (BadgeSettings{Routines: true}) {
		t.Fatalf("settings did not survive module recreation: %+v, %v", settings, err)
	}
}

func TestBadgeSettingsRejectsInvalidRequestsAndStorageErrors(t *testing.T) {
	m := fixture(t)
	for _, body := range []string{`null`, `{"sessions":"yes"}`, `{`, strings.Repeat(" ", 4096) + `{}`} {
		r := httptest.NewRequest(http.MethodPost, "http://ocman.test/api/settings/notification-badge", strings.NewReader(body))
		r.Header.Set("Origin", "http://ocman.test")
		w := httptest.NewRecorder()
		m.badgeSettings(w, r)
		if w.Code < 400 {
			t.Fatalf("invalid body accepted: %q", body)
		}
	}
	r := httptest.NewRequest(http.MethodPost, "http://ocman.test/api/settings/notification-badge", strings.NewReader(`{}`))
	r.Header.Set("Origin", "http://other.test")
	w := httptest.NewRecorder()
	m.badgeSettings(w, r)
	if w.Code != http.StatusForbidden {
		t.Fatal("cross-origin setting accepted")
	}
	if _, err := m.d.Store.SQL().Exec(`CREATE TRIGGER fail_badge_save BEFORE INSERT ON setting BEGIN SELECT RAISE(ABORT, 'cannot save'); END`); err != nil {
		t.Fatal(err)
	}
	r = httptest.NewRequest(http.MethodPost, "http://ocman.test/api/settings/notification-badge", strings.NewReader(`{}`))
	r.Header.Set("Origin", "http://ocman.test")
	w = httptest.NewRecorder()
	m.badgeSettings(w, r)
	if w.Code != http.StatusInternalServerError {
		t.Fatal("failed save acknowledged")
	}
	if _, err := m.d.Store.SQL().Exec(`DROP TRIGGER fail_badge_save`); err != nil {
		t.Fatal(err)
	}
	if err := m.d.Store.SetSetting(t.Context(), "notifications.badge", "invalid"); err != nil {
		t.Fatal(err)
	}
	w = httptest.NewRecorder()
	m.badgeSettings(w, httptest.NewRequest(http.MethodGet, "/api/settings/notification-badge", nil))
	if w.Code != http.StatusInternalServerError {
		t.Fatal("invalid saved settings silently defaulted")
	}
	if err := m.d.Store.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := m.BadgeSettings(t.Context()); err == nil {
		t.Fatal("closed store silently defaulted")
	}
}
