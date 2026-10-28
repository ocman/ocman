package protocol

import "testing"

func TestSessionStreamRequestNamespace(t *testing.T) {
	type step struct {
		d Direction
		e Envelope
	}
	run := func(id uint64) Envelope {
		e := call(id)
		e.Call.Capability, e.Call.Version, e.Call.Method = SessionCapability.Name, SessionCapability.Version, SessionRunMethod
		return e
	}
	for _, test := range []struct {
		name  string
		steps []step
		bad   bool
	}{
		{name: "duplicate plugin ID", bad: true, steps: []step{{FromPlugin, run(1)}, {FromHost, result(1)}, {FromPlugin, run(1)}}},
		{name: "decreasing plugin ID", bad: true, steps: []step{{FromPlugin, run(2)}, {FromPlugin, run(1)}}},
		{name: "plugin concurrency", bad: true, steps: []step{{FromPlugin, run(1)}, {FromPlugin, run(2)}}},
		{name: "unknown host result", bad: true, steps: []step{{FromHost, result(1)}}},
		{name: "unknown plugin cancel", bad: true, steps: []step{{FromPlugin, cancel(1)}}},
		{name: "late plugin cancel", steps: []step{{FromPlugin, run(1)}, {FromHost, result(1)}, {FromPlugin, cancel(1)}}},
	} {
		t.Run(test.name, func(t *testing.T) {
			s, err := NewStream(ModeServe, testToken, Version{1, 0}, []Capability{SessionCapability})
			if err != nil {
				t.Fatal(err)
			}
			d := sessionDescription()
			if err := s.Accept(FromPlugin, Envelope{Type: TypeHello, Hello: &Hello{Mode: ModeServe, Token: testToken, Description: &d}}); err != nil {
				t.Fatal(err)
			}
			if err := s.Accept(FromHost, acknowledgment(s)); err != nil {
				t.Fatal(err)
			}
			for i, step := range test.steps {
				err = s.Accept(step.d, step.e)
				if i < len(test.steps)-1 && err != nil {
					t.Fatalf("step %d: %v", i, err)
				}
			}
			if (err != nil) != test.bad {
				t.Fatalf("bad=%v, got %v", test.bad, err)
			}
		})
	}
}
