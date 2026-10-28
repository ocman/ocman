package local

import (
	"context"
	"io"

	"github.com/ocman/ocman/internal/composerattachments"
	"github.com/ocman/ocman/internal/hostsvc"
)

func (h *Host) SaveComposerAttachment(ctx context.Context, req hostsvc.ComposerAttachmentRequest, reader io.Reader) (*hostsvc.ComposerAttachment, error) {
	return composerattachments.Save(ctx, req, reader)
}
