package convevents

// PartData documents the frozen part schema carried in Part.Data (the
// frontend's PartData). Producers keep Data as the raw object so fields
// outside this list survive; the contract test keeps the two sides aligned.
type PartData struct {
	Type       string         `json:"type"`
	Synthetic  bool           `json:"synthetic,omitempty"`
	Ignored    bool           `json:"ignored,omitempty"`
	CallID     string         `json:"callID,omitempty"`
	Text       string         `json:"text,omitempty"`
	Tool       string         `json:"tool,omitempty"`
	Time       *PartTime      `json:"time,omitempty"`
	Permission string         `json:"permission,omitempty"`
	Patterns   []string       `json:"patterns,omitempty"`
	Reasoning  string         `json:"reasoning,omitempty"`
	ApprovedBy string         `json:"approvedBy,omitempty"`
	Reply      string         `json:"reply,omitempty"`
	Metadata   map[string]any `json:"metadata,omitempty"`
	AskedAt    int64          `json:"askedAt,omitempty"`
	ApprovedAt int64          `json:"approvedAt,omitempty"`
	Mime       string         `json:"mime,omitempty"`
	URL        string         `json:"url,omitempty"`
	Filename   string         `json:"filename,omitempty"`
	State      *PartState     `json:"state,omitempty"`
	File       string         `json:"file,omitempty"`
	Path       string         `json:"path,omitempty"`
	Content    string         `json:"content,omitempty"`
	Diff       string         `json:"diff,omitempty"`
}

type PartTime struct {
	Start int64 `json:"start,omitempty"`
	End   int64 `json:"end,omitempty"`
}

type PartState struct {
	Status   string         `json:"status,omitempty"`
	Time     *PartTime      `json:"time,omitempty"`
	Input    map[string]any `json:"input,omitempty"`
	Output   any            `json:"output,omitempty"`
	Error    string         `json:"error,omitempty"`
	Title    string         `json:"title,omitempty"`
	Metadata map[string]any `json:"metadata,omitempty"`
	// Attachments are file parts (type "file", mime, url, filename).
	Attachments []map[string]any `json:"attachments,omitempty"`
	// Raw holds streamed tool arguments (the tool.input delta field) before
	// Input is parsed.
	Raw string `json:"raw,omitempty"`
}
