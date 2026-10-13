// Package providertest runs the test-only provider executable. It never invokes
// an installed CLI agent or opens a user's database.
package providertest

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

func Directory(t testing.TB) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "sup-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return dir
}

func Binary(t testing.TB) string {
	t.Helper()
	_, source, _, _ := runtime.Caller(0)
	binary := filepath.Join(Directory(t), "fake")
	cmd := exec.Command("go", "build", "-o", binary, filepath.Join(filepath.Dir(source), "..", "testdata", "fake"))
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build fake provider: %v\n%s", err, output)
	}
	// Pay first-execution validation before a supervisor's readiness deadline.
	if output, err := exec.Command(binary, "--help").CombinedOutput(); err != nil {
		t.Fatalf("warm fake provider: %v\n%s", err, output)
	}
	return binary
}

type Process struct {
	Socket string
	Client *http.Client
	Cmd    *exec.Cmd
}

func Start(t testing.TB, binary string, version int) *Process {
	t.Helper()
	dir := Directory(t)
	p := &Process{Socket: filepath.Join(dir, "s")}
	p.Client = Client(p.Socket)
	p.Cmd = exec.Command(binary, "provider", "fake", "--socket", p.Socket, "--spool", filepath.Join(dir, "spool"), "--repo", dir, "--version", fmt.Sprint(version))
	p.Cmd.Stderr = os.Stderr
	if err := p.Cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = p.Cmd.Process.Kill(); _ = p.Cmd.Wait(); p.Client.CloseIdleConnections() })
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		resp, err := p.Client.Get("http://provider/hello")
		if err == nil {
			_ = resp.Body.Close()
			return p
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("fake provider did not become ready")
	return nil
}

func Client(socket string) *http.Client {
	return &http.Client{Transport: &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "unix", socket)
	}}}
}

func Call(t testing.TB, client *http.Client, method, path string, body, out any) int {
	t.Helper()
	data, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	req, err := http.NewRequest(method, "http://provider"+path, bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if out != nil {
		if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
			t.Fatal(err)
		}
	}
	return resp.StatusCode
}
