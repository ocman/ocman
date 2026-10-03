package local

import (
	"context"
	"io"

	"github.com/NoUseFreak/ocman/internal/composerattachments"
	"github.com/NoUseFreak/ocman/internal/hostsvc"
)

func (h *Host) SaveComposerAttachment(ctx context.Context, req hostsvc.ComposerAttachmentRequest, reader io.Reader) (*hostsvc.ComposerAttachment, error) {
	return composerattachments.Save(ctx, req, reader)
}
