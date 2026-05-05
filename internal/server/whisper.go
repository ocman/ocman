package server

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"

	log "github.com/sirupsen/logrus"
)

// executor abstracts the bits of os/exec that whisper.go uses, so the
// package can be unit-tested without the real `whisper` and `ffmpeg`
// binaries on $PATH.
//
// LookPath mirrors exec.LookPath. Run mirrors exec.CommandContext +
// cmd.Run, returning stdout and stderr as separate byte slices and any
// error from the underlying process. The interface deliberately
// matches the existing tmuxRunner pattern in tmux.go (small, with one
// production implementation and a fake for tests) — see AD-2 in the
// backend-hardening architecture doc.
type executor interface {
	LookPath(name string) (string, error)
	Run(ctx context.Context, name string, args ...string) (stdout, stderr []byte, err error)
}

// fileStat abstracts os.Stat for the model-discovery walk so tests can
// provide a virtual filesystem.
type fileStat interface {
	Stat(path string) (os.FileInfo, error)
}

// osExecutor is the production executor — calls os/exec directly.
type osExecutor struct{}

func (osExecutor) LookPath(name string) (string, error) { return exec.LookPath(name) }

func (osExecutor) Run(ctx context.Context, name string, args ...string) ([]byte, []byte, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err := cmd.Run()
	return stdout.Bytes(), stderr.Bytes(), err
}

type osFileStat struct{}

func (osFileStat) Stat(path string) (os.FileInfo, error) { return os.Stat(path) }

// knownBinaries lists the binary names to search for in $PATH.
var knownBinaries = []string{"whisper-cpp", "whisper-cli", "whisper"}

// defaultModelNames lists model filenames to look for, in preference order.
var defaultModelNames = []string{
	"ggml-base.en.bin",
	"ggml-base.bin",
	"ggml-small.en.bin",
	"ggml-small.bin",
}

// whisperNativeFormats are formats whisper-cli can read directly.
var whisperNativeFormats = map[string]bool{
	".wav": true, ".mp3": true, ".ogg": true, ".flac": true,
}

// Whisper is a self-contained transcription helper. It resolves the
// whisper / ffmpeg binaries and a model file lazily on first use; the
// resolution is then cached for the lifetime of the instance.
//
// Production code uses the package-level default created in
// init() (see [defaultWhisper]); tests construct their own with
// [newWhisper] passing in a fake [executor] and [fileStat].
type Whisper struct {
	exec executor
	fs   fileStat

	once      sync.Once
	binary    string
	model     string
	ffmpeg    string
	available bool
}

// newWhisper returns a fresh Whisper backed by the given executor and
// filesystem stub. Both must be non-nil.
func newWhisper(exec executor, fs fileStat) *Whisper {
	return &Whisper{exec: exec, fs: fs}
}

// modelSearchDirs returns directories where whisper models might live.
// It is a method (not a free function) because it can extend the
// search list with the resolved binary's own directory.
func (w *Whisper) modelSearchDirs() []string {
	home, _ := os.UserHomeDir()
	dirs := []string{
		filepath.Join(home, ".local", "share", "whisper-cpp"),
		filepath.Join(home, ".cache", "whisper"),
		"/usr/local/share/whisper-cpp",
		"/opt/homebrew/share/whisper-cpp",
	}
	// Also check next to the binary itself
	if w.binary != "" {
		binDir := filepath.Dir(w.binary)
		dirs = append(dirs, filepath.Join(binDir, "..", "share", "whisper-cpp"))
		dirs = append(dirs, filepath.Join(binDir, "models"))
	}
	return dirs
}

// init resolves the whisper binary, ffmpeg binary, and model path. It
// runs at most once per Whisper instance.
func (w *Whisper) init() {
	w.once.Do(func() {
		// Find binary
		for _, name := range knownBinaries {
			path, err := w.exec.LookPath(name)
			if err == nil {
				w.binary = path
				log.WithField("binary", path).Info("found whisper binary")
				break
			}
		}
		if w.binary == "" {
			log.Warn("whisper binary not found — voice transcription disabled")
			return
		}

		// Find model
		for _, dir := range w.modelSearchDirs() {
			for _, name := range defaultModelNames {
				path := filepath.Join(dir, name)
				if _, err := w.fs.Stat(path); err == nil {
					w.model = path
					log.WithField("model", path).Info("found whisper model")
					break
				}
			}
			if w.model != "" {
				break
			}
		}
		if w.model == "" {
			log.Warn("whisper model not found — voice transcription disabled")
			return
		}

		// Find ffmpeg for format conversion
		if path, err := w.exec.LookPath("ffmpeg"); err == nil {
			w.ffmpeg = path
			log.WithField("ffmpeg", path).Info("found ffmpeg for audio conversion")
		} else {
			log.Warn("ffmpeg not found — only wav/mp3/ogg/flac uploads will work")
		}

		w.available = true
	})
}

// Available reports whether the whisper binary and model were found.
func (w *Whisper) Available() bool {
	w.init()
	return w.available
}

// convertToWav uses ffmpeg to convert an audio file to 16kHz mono WAV.
func (w *Whisper) convertToWav(ctx context.Context, inputPath string) (string, error) {
	if w.ffmpeg == "" {
		return "", fmt.Errorf("ffmpeg not available for audio conversion")
	}
	outputPath := inputPath + ".wav"
	log.WithFields(log.Fields{"input": inputPath, "output": outputPath}).Debug("converting audio to WAV")
	_, stderr, err := w.exec.Run(ctx, w.ffmpeg,
		"-i", inputPath,
		"-ar", "16000", // 16kHz sample rate
		"-ac", "1", // mono
		"-y", // overwrite
		outputPath,
	)
	if err != nil {
		return "", fmt.Errorf("ffmpeg conversion failed: %s: %w", strings.TrimSpace(string(stderr)), err)
	}
	return outputPath, nil
}

// Transcribe runs whisper on audioPath and returns the transcribed
// text, transparently converting non-native formats via ffmpeg first.
func (w *Whisper) Transcribe(ctx context.Context, audioPath string) (string, error) {
	w.init()
	if !w.available {
		return "", fmt.Errorf("whisper is not available")
	}

	// Convert to WAV if the format is not natively supported
	ext := strings.ToLower(filepath.Ext(audioPath))
	whisperInput := audioPath
	if !whisperNativeFormats[ext] {
		converted, err := w.convertToWav(ctx, audioPath)
		if err != nil {
			return "", fmt.Errorf("audio conversion failed: %w", err)
		}
		defer os.Remove(converted)
		whisperInput = converted
	}

	log.WithField("file", whisperInput).Info("transcribing audio")

	stdout, stderr, err := w.exec.Run(ctx, w.binary,
		"-m", w.model,
		"-f", whisperInput,
		"-np",        // no extra prints
		"-nt",        // no timestamps
		"-l", "auto", // auto-detect language
	)
	if err != nil {
		stderrTrim := strings.TrimSpace(string(stderr))
		if stderrTrim != "" {
			return "", fmt.Errorf("whisper failed: %s: %w", stderrTrim, err)
		}
		return "", fmt.Errorf("whisper failed: %w", err)
	}

	text := strings.TrimSpace(string(stdout))
	log.WithField("length", len(text)).Info("transcription complete")
	return text, nil
}

// defaultWhisper is the process-wide instance used by the package
// wrappers below. It is constructed with the production executor /
// filesystem so behaviour is identical to the pre-refactor code.
var defaultWhisper = newWhisper(osExecutor{}, osFileStat{})

// whisperAvailable returns true if the whisper binary and model are found.
func whisperAvailable() bool { return defaultWhisper.Available() }

// transcribeAudio runs whisper on the given audio file and returns the text.
func transcribeAudio(audioPath string) (string, error) {
	return defaultWhisper.Transcribe(context.Background(), audioPath)
}
