package share

import (
	"fmt"
	"net/url"
	"strings"
)

// OpenStore builds a Store from a configuration string, so swapping
// backends is configuration rather than a rebuild.
//
// Supported forms:
//
//	disk:///var/lib/ocman-relay   filesystem, absolute path
//	/var/lib/ocman-relay          bare path, same as disk://
//	s3://bucket/prefix            reserved; not implemented yet
func OpenStore(spec string) (Store, error) {
	if spec == "" {
		return nil, fmt.Errorf("share: empty store specification")
	}
	// A bare path is the common case for the filesystem backend and
	// is not a valid URL, so handle it before parsing.
	if !strings.Contains(spec, "://") {
		return NewDiskStore(spec)
	}
	u, err := url.Parse(spec)
	if err != nil {
		return nil, fmt.Errorf("share: parsing store specification: %w", err)
	}
	switch u.Scheme {
	case "disk", "file":
		// disk:///var/lib/x parses with an empty Host and Path set;
		// disk://relative/path puts the first segment in Host.
		path := u.Path
		if u.Host != "" {
			path = u.Host + path
		}
		return NewDiskStore(path)
	case "s3":
		return nil, fmt.Errorf("share: s3 store is not implemented yet")
	default:
		return nil, fmt.Errorf("share: unsupported store scheme %q", u.Scheme)
	}
}
