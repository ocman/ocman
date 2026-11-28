package convstream

import (
	"encoding/json"
	"strings"
	"time"

	"github.com/ocman/ocman/internal/convevents"
)

type healthTurn struct {
	finish            func(string, time.Duration, bool)
	started           time.Time
	assistant         string
	first             time.Duration
	sawText, canceled bool
	aborts            int
	outcome           string
}

// BeginHealthCancel ties an abort receipt to this turn, not a later one.
func (h *Hub) BeginHealthCancel(key Key) func(bool) {
	h.mu.Lock()
	s := h.sessions[key]
	h.mu.Unlock()
	if s == nil {
		return nil
	}
	s.mu.Lock()
	turn := s.health
	if turn == nil {
		s.mu.Unlock()
		return nil
	}
	turn.aborts++
	s.mu.Unlock()
	return func(accepted bool) {
		s.mu.Lock()
		defer s.mu.Unlock()
		turn.aborts--
		turn.canceled = turn.canceled || accepted
		turn.settle()
	}
}

func (h *healthTurn) settle() {
	if h.outcome == "" || h.aborts != 0 {
		return
	}
	if h.canceled {
		h.outcome = "canceled"
	}
	h.finish(h.outcome, h.first, h.sawText)
}

func (s *session) observeHealth(ev convevents.Event) {
	if _, ok := ev.(convevents.TurnStarted); ok {
		s.health = nil
		if finish := s.hub.opts.BeginHealthTurn(s.key); finish != nil {
			s.health = &healthTurn{finish: finish, started: s.hub.opts.Now()}
		}
		return
	}
	h := s.health
	if h == nil {
		return
	}
	if end, ok := ev.(convevents.TurnEnded); ok {
		s.health = nil
		h.outcome = end.Outcome
		h.settle()
		return
	}
	if h.sawText {
		return
	}
	switch e := ev.(type) {
	case convevents.MessageUpserted:
		var msg struct {
			Role string `json:"role"`
		}
		if json.Unmarshal(e.Message.Data, &msg) == nil && msg.Role == "assistant" {
			h.assistant = e.Message.ID
		}
	case convevents.PartUpserted:
		if h.assistant == "" || e.Part.MessageID != h.assistant {
			return
		}
		part := readPart(e.Part.Data)
		if part.Type == "text" && strings.TrimSpace(part.Text) != "" {
			h.sawText = true
		}
	case convevents.PartDelta:
		h.sawText = h.assistant != "" && e.MessageID == h.assistant && e.PartType == "text" && e.Field == convevents.FieldText && strings.TrimSpace(e.Text) != ""
	}
	if h.sawText {
		h.first = s.hub.opts.Now().Sub(h.started)
	}
}
