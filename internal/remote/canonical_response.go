package remote

import (
	"fmt"

	"github.com/NoUseFreak/ocman/internal/db"
	"github.com/NoUseFreak/ocman/internal/platforms"
	"github.com/NoUseFreak/ocman/internal/platforms/identity"
)

func invalidCanonicalResponse() error {
	return fmt.Errorf("%w: remote violated the canonical session contract", platforms.ErrPlatformUnreachable)
}

func (p *remotePlatform) canonicalSession(row db.Session) bool {
	return identity.IsSessionID(row.ID) && (row.ParentID == "" || identity.IsSessionID(row.ParentID)) && (row.Platform == "" || row.Platform == p.base || row.Platform == string(p.ID()))
}

func (p *remotePlatform) checkedSessions(rows []db.Session, dir string, since int64) ([]db.Session, error) {
	if p.CanonicalSessionIDs() {
		for _, row := range rows {
			if !p.canonicalSession(row) {
				return nil, invalidCanonicalResponse()
			}
		}
	}
	return p.stamp(rows, dir, since), nil
}

func (p *remotePlatform) checkedStaleSessions(dir string, since int64) ([]db.Session, error) {
	rows := p.staleSessions(dir, since)
	if p.CanonicalSessionIDs() {
		for _, row := range rows {
			if !p.canonicalSession(row) {
				return nil, invalidCanonicalResponse()
			}
		}
	}
	return rows, nil
}

func (p *remotePlatform) checkedDetail(detail *platforms.SessionDetail, requested string) (*platforms.SessionDetail, error) {
	if !p.CanonicalSessionIDs() {
		return detail, nil
	}
	if detail.Session == nil || detail.Session.ID != requested || !p.canonicalSession(*detail.Session) {
		return nil, invalidCanonicalResponse()
	}
	for _, row := range detail.SessionTree {
		if !p.canonicalSession(row) {
			return nil, invalidCanonicalResponse()
		}
	}
	for _, message := range detail.Messages {
		if !identity.IsSessionID(message.SessionID) {
			return nil, invalidCanonicalResponse()
		}
	}
	for _, part := range detail.Parts {
		if !identity.IsSessionID(part.SessionID) {
			return nil, invalidCanonicalResponse()
		}
	}
	return detail, nil
}

func (p *remotePlatform) checkedSummary(row *db.Session, requested string) (*db.Session, error) {
	if p.CanonicalSessionIDs() && (row == nil || row.ID != requested || !p.canonicalSession(*row)) {
		return nil, invalidCanonicalResponse()
	}
	return row, nil
}

func (p *remotePlatform) checkedArchive(rows []db.SessionArchiveCandidate) ([]db.SessionArchiveCandidate, error) {
	if p.CanonicalSessionIDs() {
		for _, row := range rows {
			if !identity.IsSessionID(row.ID) {
				return nil, invalidCanonicalResponse()
			}
		}
	}
	return rows, nil
}

func canonicalSliceResult[T any](p *remotePlatform, rows []T) ([]T, error) {
	if !p.CanonicalSessionIDs() {
		return rows, nil
	}
	if prompts, ok := any(rows).([]platforms.LivePrompt); ok {
		for _, prompt := range prompts {
			for _, field := range []string{"sessionID", "sessionId", "session_id"} {
				if value := prompt[field]; value != nil {
					id, ok := value.(string)
					if !ok || (id != "" && !identity.IsSessionID(id)) {
						return nil, invalidCanonicalResponse()
					}
				}
			}
		}
	}
	return rows, nil
}

func canonicalJSONResult[T any](p *remotePlatform, value *T) (*T, error) {
	if !p.CanonicalSessionIDs() {
		return value, nil
	}
	valid := true
	switch result := any(value).(type) {
	case *db.Session:
		valid = p.canonicalSession(*result)
	case *platforms.CreateSessionResponse:
		valid = identity.IsSessionID(result.ID)
	case *platforms.SessionChanges:
		valid = result.SessionID == "" || identity.IsSessionID(result.SessionID)
	case *platforms.SessionInfo:
		valid = result.SessionID == "" || identity.IsSessionID(result.SessionID)
	}
	if !valid {
		return nil, invalidCanonicalResponse()
	}
	return value, nil
}

func canonicalDecodeResult[T any](p *remotePlatform, raw []byte, value *T) (*T, error) {
	err := unmarshalJSON(raw, value)
	if !p.CanonicalSessionIDs() {
		return value, err
	}
	if err == nil {
		_, err = canonicalJSONResult(p, value)
	}
	if err != nil {
		if created, ok := any(value).(*platforms.CreateSessionResponse); ok {
			id := ""
			if identity.IsSessionID(created.ID) {
				id = created.ID
			}
			return nil, &platforms.CreationPendingError{SessionID: id, Err: err}
		}
		return nil, err
	}
	return value, nil
}
