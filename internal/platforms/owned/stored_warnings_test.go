package owned

import (
	"errors"
	"testing"

	"github.com/NoUseFreak/ocman/internal/platforms"
)

type warningNative struct {
	*nativePlatform
	directories []string
}

func (p *warningNative) StoredReadConfig(directory string) *platforms.ConversationHead {
	p.directories = append(p.directories, directory)
	head := &platforms.ConversationHead{DirectoryDefaults: true}
	if directory != "" {
		head.Warnings = []platforms.SessionWarning{{Kind: "duplicate_opencode_servers", Ports: []string{"1", "2"}}}
	}
	return head
}

// Directory warnings are current observations: a stored conversation from the
// live source carries them, a disconnected or replaced source never does.
func TestStoredConversationCarriesCurrentDirectoryWarnings(t *testing.T) {
	a, p, id := storedFixture(t)
	native := &warningNative{nativePlatform: p}
	a.native = native
	detail, err := a.Session(t.Context(), id, 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(detail.Warnings) != 1 || detail.Warnings[0].Kind != "duplicate_opencode_servers" {
		t.Fatalf("live directory warning lost: %+v (asked %q)", detail.Warnings, native.directories)
	}
	if native.directories[len(native.directories)-1] != "/stored" {
		t.Fatalf("warnings requested for %q, want the stored directory", native.directories)
	}
	a.unavailable = errors.New("source disconnected")
	detail, err = a.Session(t.Context(), id, 10, 0)
	if err != nil || len(detail.Warnings) != 0 {
		t.Fatalf("offline source reported live warnings: %+v %v", detail.Warnings, err)
	}
}
