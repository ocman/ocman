package hostsvc

// ComposerAttachmentRequest contains owner-local metadata; file bytes travel
// separately so the remote RPC can stream files larger than a unary message.
type ComposerAttachmentRequest struct {
	Directory string `json:"directory"`
	SessionID string `json:"sessionId"`
	Name      string `json:"name"`
	Mime      string `json:"mime"`
}

type ComposerAttachment struct {
	Path string `json:"path"`
	Name string `json:"name"`
	Mime string `json:"mime"`
	Size int64  `json:"size"`
}
