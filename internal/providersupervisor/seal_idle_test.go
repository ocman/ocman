package providersupervisor

import (
	"strings"
	"testing"
)

func TestSealIdlePreventsConcurrentResume(t *testing.T) {
	for _, resumeFirst := range []bool{false, true} {
		s := supervisor(t)
		request(s, "POST", "/drain-idle?token=owner", "")
		if resumeFirst {
			request(s, "POST", "/resume?token=owner", "")
			if err := s.BeginTurn("s", nil); err != nil {
				t.Fatal(err)
			}
		}
		w := request(s, "POST", "/seal-idle?token=owner", "")
		if strings.Contains(w.Body.String(), `"idle":true`) == resumeFirst {
			t.Fatalf("resumeFirst=%v, seal=%s", resumeFirst, w.Body.String())
		}
		if !resumeFirst {
			request(s, "POST", "/resume?token=owner", "")
			if err := s.BeginTurn("s", nil); err == nil {
				t.Fatal("resume undid a sealed drain")
			}
		}
	}
}

func TestSealIdleRequiresExactIdlePause(t *testing.T) {
	for _, state := range []string{"undrained", "wrong token", "empty token", "replacement", "admitted", "active", "limited", "prompt"} {
		t.Run(state, func(t *testing.T) {
			s := supervisor(t)
			if state != "undrained" {
				request(s, "POST", "/drain-idle?token=owner", "")
			}
			token := "owner"
			switch state {
			case "wrong token":
				token = "wrong"
			case "empty token":
				token = ""
			case "replacement":
				request(s, "POST", "/drain", "")
			case "admitted":
				s.admitted = 1
			case "active":
				s.session("s").active = true
			case "limited":
				s.session("s").limited = true
			case "prompt":
				publish(t, s, "permission.asked", `{"sessionID":"s","id":"p"}`)
			}
			if w := request(s, "POST", "/seal-idle?token="+token, ""); !strings.Contains(w.Body.String(), `"idle":false`) {
				t.Fatalf("unsafe seal: %s", w.Body.String())
			}
		})
	}
}
