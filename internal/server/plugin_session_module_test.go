package server

import (
	"github.com/NoUseFreak/ocman/internal/pluginsmod"

	"testing"

	"github.com/NoUseFreak/ocman/internal/plugins"
)

func TestPluginSessionModuleCompletionTriggers(t *testing.T) {
	for _, trigger := range []string{"idle bus", "terminal change", "module runner"} {
		t.Run(trigger, func(t *testing.T) {
			f := newSessionFixture(t)
			id := sessionPluginDescription().ID
			f.install(t, map[string]string{"project": f.project}, []string{plugins.SessionRunGrant})
			f.awaitReady(t)
			if err := pluginsmod.NewStore(f.s.stateDB).LinkPluginSession(t.Context(), "opencode", "ses-run-1", id); err != nil {
				t.Fatal(err)
			}
			switch trigger {
			case "idle bus":
				f.s.onSessionIdle("opencode", "ses-run-1")
			case "terminal change":
				f.s.broadcastSessionChanged("ses-run-1")
			case "module runner":
				runPluginsModule(t, f.s)
			}
			f.awaitRecord(t, "completed\tses-run-1\tsess-out:1\tanswer\n")
			if has, err := pluginsmod.NewStore(f.s.stateDB).HasPluginSessionCompletion(t.Context(), id, "ses-run-1:m2"); err != nil || !has {
				t.Fatalf("durable completion receipt: %v %v", has, err)
			}
		})
	}
}
