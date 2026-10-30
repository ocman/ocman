package codexprovider

import (
	"context"
	"os/exec"
	"strings"
	"time"
)

func now() int64 { return time.Now().UnixMilli() }

// codexVersion reports the installed CLI version without requiring one.
func codexVersion(cli string) string {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, cli, "--version").Output()
	if fields := strings.Fields(string(out)); err == nil && len(fields) > 0 {
		return fields[len(fields)-1]
	}
	return "unavailable"
}
