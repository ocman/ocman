// Package usagewire defines the complete, deliberately small usage report.
package usagewire

import (
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"
)

// Report contains no conversation, account, machine or device metadata.
type Report struct {
	InstallationID string `json:"installationId"`
	Date           string `json:"date"`
	Version        string `json:"version"`
}

var releaseVersion = regexp.MustCompile(`^[0-9]{1,6}\.[0-9]{1,6}\.[0-9]{1,6}(-(dev|rc|beta|alpha)\.[0-9]{1,6})?$`)

// NormalizeVersion keeps release versions, but never custom build labels,
// branch names, commit hashes or build metadata.
func NormalizeVersion(version string) string {
	version = strings.TrimPrefix(version, "v")
	if releaseVersion.MatchString(version) {
		return version
	}
	return "dev"
}

func (r Report) Valid(now time.Time) bool {
	id, err := uuid.Parse(r.InstallationID)
	if err != nil || id.String() != r.InstallationID || id.Version() != 4 || id.Variant() != uuid.RFC4122 {
		return false
	}
	day, err := time.Parse(time.DateOnly, r.Date)
	if err != nil || day.Format(time.DateOnly) != r.Date {
		return false
	}
	today := now.UTC().Format(time.DateOnly)
	yesterday := now.UTC().AddDate(0, 0, -1).Format(time.DateOnly)
	return (r.Date == today || r.Date == yesterday) && r.Version == NormalizeVersion(r.Version)
}
