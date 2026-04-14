package server

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"

	log "github.com/sirupsen/logrus"
)

// whisperState caches the resolved binary path and model path.
var whisperState struct {
	once      sync.Once
	binary    string
	model     string
	ffmpeg    string
	available bool
}

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

// modelSearchDirs returns directories where whisper models might live.
func modelSearchDirs() []string {
	home, _ := os.UserHomeDir()
	dirs := []string{
		filepath.Join(home, ".local", "share", "whisper-cpp"),
		filepath.Join(home, ".cache", "whisper"),
		"/usr/local/share/whisper-cpp",
		"/opt/homebrew/share/whisper-cpp",
	}
	// Also check next to the binary itself
	if whisperState.binary != "" {
		binDir := filepath.Dir(whisperState.binary)
		dirs = append(dirs, filepath.Join(binDir, "..", "share", "whisper-cpp"))
		dirs = append(dirs, filepath.Join(binDir, "models"))
	}
	return dirs
}

// initWhisper resolves the whisper binary and model paths once.
func initWhisper() {
	whisperState.once.Do(func() {
		// Find binary
		for _, name := range knownBinaries {
			path, err := exec.LookPath(name)
			if err == nil {
				whisperState.binary = path
				log.WithField("binary", path).Info("found whisper binary")
				break
			}
		}
		if whisperState.binary == "" {
			log.Warn("whisper binary not found — voice transcription disabled")
			return
		}

		// Find model
		for _, dir := range modelSearchDirs() {
			for _, name := range defaultModelNames {
				path := filepath.Join(dir, name)
				if _, err := os.Stat(path); err == nil {
					whisperState.model = path
					log.WithField("model", path).Info("found whisper model")
					break
				}
			}
			if whisperState.model != "" {
				break
			}
		}
		if whisperState.model == "" {
			log.Warn("whisper model not found — voice transcription disabled")
			return
		}

		// Find ffmpeg for format conversion
		if path, err := exec.LookPath("ffmpeg"); err == nil {
			whisperState.ffmpeg = path
			log.WithField("ffmpeg", path).Info("found ffmpeg for audio conversion")
		} else {
			log.Warn("ffmpeg not found — only wav/mp3/ogg/flac uploads will work")
		}

		whisperState.available = true
	})
}

// whisperAvailable returns true if the whisper binary and model are found.
func whisperAvailable() bool {
	initWhisper()
	return whisperState.available
}

// convertToWav uses ffmpeg to convert an audio file to 16kHz mono WAV
// (the format whisper expects). Returns the path to the converted file.
func convertToWav(inputPath string) (string, error) {
	if whisperState.ffmpeg == "" {
		return "", fmt.Errorf("ffmpeg not available for audio conversion")
	}

	outputPath := inputPath + ".wav"
	cmd := exec.Command(
		whisperState.ffmpeg,
		"-i", inputPath,
		"-ar", "16000", // 16kHz sample rate
		"-ac", "1", // mono
		"-y", // overwrite
		outputPath,
	)

	log.WithFields(log.Fields{"input": inputPath, "output": outputPath}).Debug("converting audio to WAV")

	if out, err := cmd.CombinedOutput(); err != nil {
		return "", fmt.Errorf("ffmpeg conversion failed: %s", string(out))
	}
	return outputPath, nil
}

// transcribeAudio runs whisper on the given audio file and returns the text.
// If the file is not in a format whisper supports natively, it will be
// converted to WAV via ffmpeg first.
func transcribeAudio(audioPath string) (string, error) {
	initWhisper()
	if !whisperState.available {
		return "", fmt.Errorf("whisper is not available")
	}

	// Convert to WAV if the format is not natively supported
	ext := strings.ToLower(filepath.Ext(audioPath))
	whisperInput := audioPath
	if !whisperNativeFormats[ext] {
		converted, err := convertToWav(audioPath)
		if err != nil {
			return "", fmt.Errorf("audio conversion failed: %w", err)
		}
		defer os.Remove(converted)
		whisperInput = converted
	}

	cmd := exec.Command(
		whisperState.binary,
		"-m", whisperState.model,
		"-f", whisperInput,
		"-np",        // no extra prints
		"-nt",        // no timestamps
		"-l", "auto", // auto-detect language
	)

	log.WithField("file", whisperInput).Info("transcribing audio")

	out, err := cmd.Output()
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			return "", fmt.Errorf("whisper failed: %s", string(exitErr.Stderr))
		}
		return "", fmt.Errorf("whisper failed: %w", err)
	}

	text := strings.TrimSpace(string(out))
	log.WithField("length", len(text)).Info("transcription complete")
	return text, nil
}
