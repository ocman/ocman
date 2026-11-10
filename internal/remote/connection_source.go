package remote

// CanonicalSource is immutable authenticated handshake metadata. It performs
// no RPC, so registry source resolution remains safe while holding its lock.
func (p *remotePlatform) CanonicalSource() (owner, kind, source string) {
	return p.remoteID(), string(p.NativeKind()), p.sourceKey
}

// CanonicalSources includes retained read-only sources, not execution grants.
// Older peers advertise only the singular active source.
func (p *remotePlatform) CanonicalSources() (owner, kind string, sources []string) {
	if len(p.sourceKeys) > 0 {
		return p.remoteID(), string(p.NativeKind()), append([]string(nil), p.sourceKeys...)
	}
	if p.sourceKey != "" {
		return p.remoteID(), string(p.NativeKind()), []string{p.sourceKey}
	}
	return p.remoteID(), string(p.NativeKind()), nil
}
