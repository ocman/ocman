package convevents

import "github.com/ocman/ocman/internal/db"

// Inflight is the current turn's content as the live source sees it.
type Inflight struct {
	Messages []db.Message `json:"messages"`
	Parts    []Part       `json:"parts"`
}

// LiveOverlay restates every live state on a cold or unresumed connect.
type LiveOverlay struct {
	Status         SessionStatusEvent `json:"status"`
	Prompts        []Prompt           `json:"prompts"`
	Judge          []PromptJudge      `json:"judge"`
	Background     BackgroundChanged  `json:"background"`
	Queue          *QueueChanged      `json:"queue,omitempty"`
	Inflight       Inflight           `json:"inflight"`
	LiveConnection bool               `json:"liveConnection"`
}

type StreamHello struct {
	Protocol int `json:"protocol"`
	// Resumed is true when the cursor was honoured and replay follows.
	Resumed  bool         `json:"resumed"`
	Overlay  *LiveOverlay `json:"overlay,omitempty"`
	Revision int64        `json:"revision"`
}

// Stream reset reasons.
const (
	ResetEpoch           = "epoch"
	ResetEvicted         = "evicted"
	ResetSlowConsumer    = "slow-consumer"
	ResetSourceRestarted = "source-restarted"
)

type StreamReset struct {
	Reason string `json:"reason"`
}

// Stream error codes.
const (
	CodePlatformUnreachable = "platform-unreachable"
	CodeOwnerOffline        = "owner-offline"
	CodeAuth                = "auth"
	CodeInternal            = "internal"
)

type StreamError struct {
	Code      string `json:"code"`
	Message   string `json:"message"`
	Retryable bool   `json:"retryable"`
}

// StreamCapabilities are the OCE capability flags (design §4.8). They are
// not yet part of platforms.Capabilities.
type StreamCapabilities struct {
	StreamResume bool     `json:"streamResume"`
	DeltaFields  []string `json:"deltaFields"`
}
