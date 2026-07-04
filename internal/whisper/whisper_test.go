package whisper

import (
	"context"
	"errors"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"
)

// fakeExecutor is a recording stub for the whisper executor. Tests
// configure LookPath / Run via the result maps + handler functions.
type fakeExecutor struct {
	// lookPaths maps an argument to LookPath ("whisper" / "ffmpeg" /
	// etc) to the result returned. A missing key produces an "exec:
	// not found"-style error so the caller can simulate a missing
	// binary.
	lookPaths map[string]string

	// runHandler is invoked from Run. If nil, Run returns ("", "", nil).
	// Tests use it to assert on the args list and return canned
	// stdout/stderr/exit-error.
	runHandler func(ctx context.Context, name string, args []string) (stdout, stderr []byte, err error)

	// runs records every Run call in order.
	runs []fakeExecRun
}

type fakeExecRun struct {
	Name string
	Args []string
}

func (f *fakeExecutor) LookPath(name string) (string, error) {
	if f.lookPaths == nil {
		return "", errors.New("exec: \"" + name + "\": not found")
	}
	if v, ok := f.lookPaths[name]; ok {
		return v, nil
	}
	return "", errors.New("exec: \"" + name + "\": not found")
}

func (f *fakeExecutor) Run(ctx context.Context, name string, args ...string) ([]byte, []byte, error) {
	f.runs = append(f.runs, fakeExecRun{Name: name, Args: append([]string(nil), args...)})
	if f.runHandler != nil {
		return f.runHandler(ctx, name, args)
	}
	return nil, nil, nil
}

// fakeFS reports a configurable set of files as existing. Anything not
// in `paths` returns os.ErrNotExist.
type fakeFS struct {
	paths map[string]bool
}

func (f *fakeFS) Stat(path string) (os.FileInfo, error) {
	if f.paths[path] {
		// FileInfo isn't inspected by Whisper.init — only the err.
		return fakeFileInfo{name: path}, nil
	}
	return nil, os.ErrNotExist
}

type fakeFileInfo struct{ name string }

func (f fakeFileInfo) Name() string       { return f.name }
func (f fakeFileInfo) Size() int64        { return 0 }
func (f fakeFileInfo) Mode() os.FileMode  { return 0 }
func (f fakeFileInfo) ModTime() time.Time { return time.Time{} }
func (f fakeFileInfo) IsDir() bool        { return false }
func (f fakeFileInfo) Sys() any           { return nil }

func TestWhisper_AvailableFalse_NoBinary(t *testing.T) {
	w := newWhisper(&fakeExecutor{lookPaths: nil}, &fakeFS{})
	if w.Available() {
		t.Fatalf("Available = true with no binary on PATH; want false")
	}
}

func TestWhisper_AvailableFalse_NoModel(t *testing.T) {
	w := newWhisper(&fakeExecutor{lookPaths: map[string]string{
		"whisper-cpp": "/usr/local/bin/whisper-cpp",
	}}, &fakeFS{})
	if w.Available() {
		t.Fatalf("Available = true with no model on disk; want false")
	}
}

func TestWhisper_AvailableTrue(t *testing.T) {
	home, _ := os.UserHomeDir()
	modelPath := home + "/.local/share/whisper-cpp/ggml-base.en.bin"
	w := newWhisper(
		&fakeExecutor{lookPaths: map[string]string{
			"whisper-cpp": "/opt/homebrew/bin/whisper-cpp",
			"ffmpeg":      "/opt/homebrew/bin/ffmpeg",
		}},
		&fakeFS{paths: map[string]bool{modelPath: true}},
	)
	if !w.Available() {
		t.Fatalf("Available = false; want true (binary + model + ffmpeg all present)")
	}
}

func TestWhisper_ConvertToWav_InvokesFfmpeg(t *testing.T) {
	home, _ := os.UserHomeDir()
	modelPath := home + "/.local/share/whisper-cpp/ggml-base.en.bin"
	exec := &fakeExecutor{
		lookPaths: map[string]string{
			"whisper-cpp": "/whisper",
			"ffmpeg":      "/ffmpeg",
		},
	}
	w := newWhisper(exec, &fakeFS{paths: map[string]bool{modelPath: true}})
	w.init()

	out, err := w.convertToWav(context.Background(), "/tmp/in.m4a")
	if err != nil {
		t.Fatalf("convertToWav: %v", err)
	}
	if out != "/tmp/in.m4a.wav" {
		t.Fatalf("output path = %q, want /tmp/in.m4a.wav", out)
	}
	if len(exec.runs) != 1 {
		t.Fatalf("expected 1 ffmpeg run, got %d", len(exec.runs))
	}
	got := exec.runs[0]
	if got.Name != "/ffmpeg" {
		t.Fatalf("ran %q, want /ffmpeg", got.Name)
	}
	wantArgs := []string{"-i", "/tmp/in.m4a", "-ar", "16000", "-ac", "1", "-y", "/tmp/in.m4a.wav"}
	if !reflect.DeepEqual(got.Args, wantArgs) {
		t.Fatalf("ffmpeg args = %v, want %v", got.Args, wantArgs)
	}
}

func TestWhisper_Transcribe_NativeFormat(t *testing.T) {
	home, _ := os.UserHomeDir()
	modelPath := home + "/.local/share/whisper-cpp/ggml-base.en.bin"
	exec := &fakeExecutor{
		lookPaths: map[string]string{
			"whisper-cpp": "/whisper",
			"ffmpeg":      "/ffmpeg",
		},
		runHandler: func(_ context.Context, name string, args []string) ([]byte, []byte, error) {
			return []byte("  hello world  \n"), nil, nil
		},
	}
	w := newWhisper(exec, &fakeFS{paths: map[string]bool{modelPath: true}})

	text, err := w.Transcribe(context.Background(), "/tmp/in.wav")
	if err != nil {
		t.Fatalf("Transcribe: %v", err)
	}
	if text != "hello world" {
		t.Fatalf("text = %q, want %q", text, "hello world")
	}
	if len(exec.runs) != 1 {
		t.Fatalf("expected 1 run (no ffmpeg conversion for .wav); got %d", len(exec.runs))
	}
	got := exec.runs[0]
	if got.Name != "/whisper" {
		t.Fatalf("ran %q, want /whisper", got.Name)
	}
	wantArgs := []string{"-m", modelPath, "-f", "/tmp/in.wav", "-np", "-nt", "-l", "auto"}
	if !reflect.DeepEqual(got.Args, wantArgs) {
		t.Fatalf("whisper args = %v, want %v", got.Args, wantArgs)
	}
}

func TestWhisper_Transcribe_NonNativeFormatTriggersFfmpeg(t *testing.T) {
	home, _ := os.UserHomeDir()
	modelPath := home + "/.local/share/whisper-cpp/ggml-base.en.bin"
	exec := &fakeExecutor{
		lookPaths: map[string]string{
			"whisper-cpp": "/whisper",
			"ffmpeg":      "/ffmpeg",
		},
		runHandler: func(_ context.Context, name string, args []string) ([]byte, []byte, error) {
			if name == "/ffmpeg" {
				// Need to actually create the file so os.Remove
				// in defer doesn't blow up — but it's allowed to
				// fail silently, so an empty fake is fine.
				return nil, nil, nil
			}
			return []byte("transcribed"), nil, nil
		},
	}
	w := newWhisper(exec, &fakeFS{paths: map[string]bool{modelPath: true}})

	_, err := w.Transcribe(context.Background(), "/tmp/in.m4a")
	if err != nil {
		t.Fatalf("Transcribe: %v", err)
	}
	if len(exec.runs) != 2 {
		t.Fatalf("expected 2 runs (ffmpeg then whisper); got %d", len(exec.runs))
	}
	if exec.runs[0].Name != "/ffmpeg" {
		t.Fatalf("first call = %q, want /ffmpeg", exec.runs[0].Name)
	}
	if exec.runs[1].Name != "/whisper" {
		t.Fatalf("second call = %q, want /whisper", exec.runs[1].Name)
	}
}

func TestWhisper_Transcribe_SurfacesStderr(t *testing.T) {
	home, _ := os.UserHomeDir()
	modelPath := home + "/.local/share/whisper-cpp/ggml-base.en.bin"
	exec := &fakeExecutor{
		lookPaths: map[string]string{
			"whisper-cpp": "/whisper",
		},
		runHandler: func(_ context.Context, name string, args []string) ([]byte, []byte, error) {
			return nil, []byte("model file is corrupt"), errors.New("exit status 1")
		},
	}
	w := newWhisper(exec, &fakeFS{paths: map[string]bool{modelPath: true}})

	_, err := w.Transcribe(context.Background(), "/tmp/in.wav")
	if err == nil {
		t.Fatalf("Transcribe: want error, got nil")
	}
	if !strings.Contains(err.Error(), "model file is corrupt") {
		t.Fatalf("error %q does not include captured stderr", err.Error())
	}
}

func TestWhisper_Transcribe_NotAvailable(t *testing.T) {
	w := newWhisper(&fakeExecutor{}, &fakeFS{})
	_, err := w.Transcribe(context.Background(), "/tmp/in.wav")
	if err == nil {
		t.Fatalf("Transcribe with no binary: want error, got nil")
	}
}
