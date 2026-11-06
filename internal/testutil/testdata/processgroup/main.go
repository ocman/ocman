// Command processgroup execs a test provider in its own process group, so the
// fake tmux can stop the whole session rather than orphaning its agent children.
package main

import (
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"syscall"

	"github.com/ocman/ocman/internal/testutil"
)

func main() {
	if len(os.Args) == 1 { // warm up the executable before runtime deadlines
		return
	}
	var err error
	if os.Args[1] == "--stop" && len(os.Args) == 3 {
		var pid int
		pid, err = strconv.Atoi(os.Args[2])
		if err == nil {
			err = testutil.StopProcessGroup(pid)
		}
	} else {
		args := os.Args[1:]
		var pidFile string
		if len(args) > 2 && args[0] == "--pid-file" {
			pidFile, args = args[1], args[2:]
		}
		var binary string
		binary, err = exec.LookPath(args[0])
		if err == nil {
			err = syscall.Setpgid(0, 0)
		}
		if err == nil && pidFile != "" {
			err = os.WriteFile(pidFile, []byte(strconv.Itoa(os.Getpid())), 0o600)
		}
		if err == nil {
			err = syscall.Exec(binary, args, os.Environ())
		}
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
