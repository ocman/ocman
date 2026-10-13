package providersupervisor

import (
	"net/http"
	"strings"
	"testing"
)

func TestIdleDrainDoesNotBlockBusyOrPromptedSupervisor(t *testing.T) {
	for _, busy := range []string{"turn", "permission", "question", "limited"} {
		t.Run(busy, func(t *testing.T) {
			s := supervisor(t)
			switch busy {
			case "turn":
				if err := s.BeginTurn("s", nil); err != nil {
					t.Fatal(err)
				}
			case "permission":
				publish(t, s, "permission.asked", `{"sessionID":"s","id":"p"}`)
			case "question":
				publish(t, s, "question.asked", `{"sessionID":"s","id":"q"}`)
			case "limited":
				publish(t, s, "session.status", `{"sessionID":"s","status":{"type":"limited"}}`)
			}
			w := request(s, "POST", "/drain-idle", "")
			if !strings.Contains(w.Body.String(), `"idle":false`) {
				t.Fatal(w.Body.String())
			}
			if err := s.BeginTurn("next", nil); err != nil {
				t.Fatalf("busy cull blocked admission: %v", err)
			}
		})
	}
}

func TestIdleDrainFencesAdmissionAndCanResume(t *testing.T) {
	s := supervisor(t)
	w := request(s, "POST", "/drain-idle", "")
	if !strings.Contains(w.Body.String(), `"idle":true`) {
		t.Fatal(w.Body.String())
	}
	if w := request(s, "POST", "/sessions/s/send", ""); w.Code != http.StatusConflict {
		t.Fatal(w.Code)
	}
	if w := request(s, "POST", "/resume", ""); w.Code != http.StatusOK {
		t.Fatal(w.Code)
	}
	if err := s.BeginTurn("s", nil); err != nil {
		t.Fatal(err)
	}
}

func TestIdleDrainPreservesAdmittedRequest(t *testing.T) {
	s := supervisor(t)
	entered, release, done := make(chan struct{}), make(chan struct{}), make(chan struct{})
	s.backend.Handler = http.HandlerFunc(func(http.ResponseWriter, *http.Request) { close(entered); <-release })
	go func() { request(s, "POST", "/sessions/s/send", ""); close(done) }()
	<-entered
	w := request(s, "POST", "/drain-idle", "")
	close(release)
	<-done
	if !strings.Contains(w.Body.String(), `"idle":false`) {
		t.Fatal(w.Body.String())
	}
	if err := s.BeginTurn("next", nil); err != nil {
		t.Fatal(err)
	}
}
