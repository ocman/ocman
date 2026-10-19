package platforms

import (
	"context"

	"github.com/NoUseFreak/ocman/internal/db"
)

// ConversationHead contains only bounded source metadata and current lifecycle
// authority. Implementations must not read transcript content or launch agents.
type ConversationHead struct {
	// LiveOnly identifies an owned runtime whose transcript has not been
	// persisted yet. Its content is still part of the live execution path.
	LiveOnly bool
	Pricing  interface {
		CalcCost(string, int64, int64, int64, int64) float64
	}
	Identity          HistoryIdentity
	Version           string
	Session           *db.Session
	DefaultModel      string
	DirectoryDefaults bool
	Warnings          []SessionWarning
}

type ConversationHeadReader interface {
	ConversationHead(context.Context, string) (*ConversationHead, error)
}

// ConversationReader is installed only on browsing contexts. Mutation, queue,
// lifecycle and streaming contexts retain their original platform behavior.
type ConversationReader interface {
	ReadConversation(context.Context, Platform, string, int, int) (*SessionDetail, bool, error)
}

type conversationReaderKey struct{}

func WithConversationReader(ctx context.Context, reader ConversationReader) context.Context {
	return context.WithValue(ctx, conversationReaderKey{}, reader)
}

// IsConversationRead preserves browsing intent when a read crosses an owner RPC.
func IsConversationRead(ctx context.Context) bool {
	reader, ok := ctx.Value(conversationReaderKey{}).(ConversationReader)
	return ok && reader != nil
}

func TryStoredConversation(ctx context.Context, p Platform, id string, limit, offset int) (*SessionDetail, bool, error) {
	reader, ok := ctx.Value(conversationReaderKey{}).(ConversationReader)
	if !ok || reader == nil {
		return nil, false, nil
	}
	return reader.ReadConversation(ctx, p, id, limit, offset)
}
