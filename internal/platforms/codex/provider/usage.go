package codexprovider

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"strings"

	"github.com/klauspost/compress/zstd"

	codexappserver "github.com/NoUseFreak/ocman/internal/platforms/codex/appserver"
)

// TurnUsage is what a turn cost and how full the context was at its end.
type TurnUsage struct {
	Tokens  codexappserver.TokenUsage // the whole turn, every model request
	Context int64                     // the last request's input and output
	Window  int64                     // the model's context window; 0 unknown
}

// rolloutUsage is the snake_case usage of a rollout record.
type rolloutUsage struct {
	Input           int64 `json:"input_tokens"`
	CachedInput     int64 `json:"cached_input_tokens"`
	CacheWriteInput int64 `json:"cache_write_input_tokens"`
	Output          int64 `json:"output_tokens"`
	ReasoningOutput int64 `json:"reasoning_output_tokens"`
	Total           int64 `json:"total_tokens"`
}

func (u rolloutUsage) usage() codexappserver.TokenUsage {
	return codexappserver.TokenUsage{TotalTokens: u.Total, InputTokens: u.Input, CachedInputTokens: u.CachedInput,
		CacheWriteInputTokens: u.CacheWriteInput, OutputTokens: u.Output, ReasoningOutputTokens: u.ReasoningOutput}
}

// ReadRolloutUsage reads per-turn token usage from a thread's rollout file,
// the only place Codex keeps it: token_usage_record lines carry each model
// request's usage and the turn's running total, task_started the model's
// context window. Cold rollouts are zstd-compressed (.jsonl.zst). Unknown
// and malformed lines are skipped; a missing file is an error.
func ReadRolloutUsage(path string) (map[string]TurnUsage, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var r io.Reader = f
	if strings.HasSuffix(path, ".zst") {
		dec, err := zstd.NewReader(f)
		if err != nil {
			return nil, err
		}
		defer dec.Close()
		r = dec
	}
	out := map[string]TurnUsage{}
	reader := bufio.NewReaderSize(r, 64<<10)
	for {
		line, err := reader.ReadBytes('\n')
		addUsageLine(out, line)
		if errors.Is(err, io.EOF) {
			return out, nil
		}
		if err != nil {
			return nil, err
		}
	}
}

func addUsageLine(out map[string]TurnUsage, line []byte) {
	// Most lines are conversation content; decode only the two kinds read.
	usage, started := bytes.Contains(line, []byte(`"token_usage_record"`)), bytes.Contains(line, []byte(`"task_started"`))
	if !usage && !started {
		return
	}
	var rec struct {
		Type    string `json:"type"`
		Payload struct {
			Type      string       `json:"type"`
			TurnID    string       `json:"turn_id"`
			Usage     rolloutUsage `json:"usage"`
			TurnUsage rolloutUsage `json:"turn_token_usage"`
			Window    int64        `json:"model_context_window"`
		} `json:"payload"`
	}
	if json.Unmarshal(line, &rec) != nil || rec.Payload.TurnID == "" {
		return
	}
	p, u := rec.Payload, out[rec.Payload.TurnID]
	switch {
	case rec.Type == "token_usage_record":
		u.Tokens, u.Context = p.TurnUsage.usage(), p.Usage.Input+p.Usage.Output
	case rec.Type == "event_msg" && p.Type == "task_started":
		u.Window = p.Window
	default:
		return
	}
	out[p.TurnID] = u
}

// applyUsage records a turn's usage on its reply: tokens for analytics, and
// the context it ended with for the session info panel.
func applyUsage(info map[string]any, u TurnUsage) {
	info["tokens"] = Tokens(u.Tokens)
	info["contextTokens"] = u.Context
	if u.Window > 0 {
		info["contextLimit"] = u.Window
	}
}
