package opencode

import (
	"strings"

	"github.com/sirupsen/logrus"
	logtest "github.com/sirupsen/logrus/hooks/test"
)

func findLog(hook *logtest.Hook, level logrus.Level, substr string) bool {
	for _, e := range hook.AllEntries() {
		if e.Level == level && strings.Contains(e.Message, substr) {
			return true
		}
	}
	return false
}
