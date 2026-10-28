package platforms

import "github.com/ocman/ocman/internal/db"

// ConversationHead carries the platform configuration a stored conversation
// read needs: pricing, composer defaults and warnings. It never contains
// native transcript content.
type ConversationHead struct {
	Pricing interface {
		CalcCost(string, int64, int64, int64, int64) float64
	}
	Session           *db.Session
	DefaultModel      string
	DirectoryDefaults bool
	Warnings          []SessionWarning
}
