package platforms

import "github.com/ocman/ocman/internal/db"

// SessionCreator identifies the session that launched this independent session.
// It does not imply native ancestry, inherited permissions or shared lifecycle.
type SessionCreator = db.SessionCreator
