package providersupervisor

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/NoUseFreak/ocman/internal/providerprotocol"
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
			w := request(s, "POST", "/drain-idle?token=cleanup", "")
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
	w := request(s, "POST", "/drain-idle?token=cleanup", "")
	if !strings.Contains(w.Body.String(), `"idle":true`) {
		t.Fatal(w.Body.String())
	}
	if w := request(s, "POST", "/sessions/s/send", ""); w.Code != http.StatusConflict {
		t.Fatal(w.Code)
	}
	if w := request(s, "POST", "/resume?token=cleanup", ""); w.Code != http.StatusOK {
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
	w := request(s, "POST", "/drain-idle?token=cleanup", "")
	close(release)
	<-done
	if !strings.Contains(w.Body.String(), `"idle":false`) {
		t.Fatal(w.Body.String())
	}
	if err := s.BeginTurn("next", nil); err != nil {
		t.Fatal(err)
	}
}

func TestIdleResumeDoesNotUndoReplacementDrain(t *testing.T) {
	s := supervisor(t)
	request(s, "POST", "/drain-idle?token=cleanup", "")
	request(s, "POST", "/drain", "")
	request(s, "POST", "/resume?token=cleanup", "")
	if err := s.BeginTurn("next", nil); err == nil {
		t.Fatal("cleanup rollback undid a replacement drain")
	}
}

func TestIdleDrainTokenRecoveryAndValidation(t *testing.T) {
	s := supervisor(t)
	for _, token := range []string{"", strings.Repeat("a", 129)} {
		if w := request(s, "POST", "/drain-idle?token="+token, ""); w.Code != 400 {
			t.Fatal("invalid token accepted", w.Code)
		}
	}
	request(s, "POST", "/drain-idle?token=cleanup", "")
	var hello providerprotocol.Hello
	if err := json.Unmarshal(request(s, "GET", "/hello", "").Body.Bytes(), &hello); err != nil {
		t.Fatal(err)
	}
	if hello.IdleDrainToken != "cleanup" {
		t.Fatal("interrupted cleanup not discoverable", hello)
	}
	request(s, "POST", "/resume?token=wrong", "")
	if err := s.BeginTurn("next", nil); err == nil {
		t.Fatal("unrelated rollback resumed cleanup")
	}
	if w := request(s, "POST", "/drain-idle?token=other", ""); !strings.Contains(w.Body.String(), `"idle":false`) {
		t.Fatal("stole cleanup pause")
	}
	request(s, "POST", "/resume?token=cleanup", "")
	if err := s.BeginTurn("next", nil); err != nil {
		t.Fatal(err)
	}
}
