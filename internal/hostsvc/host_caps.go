package hostsvc

import "encoding/json"

// UnmarshalJSON also accepts SessionLaunch under its former wire name,
// "opencodeLaunch", which remotes on the same protocol version may send.
func (c *HostCaps) UnmarshalJSON(data []byte) error {
	type plain HostCaps
	var wire struct {
		plain
		SessionLaunch *bool `json:"sessionLaunch"`
		Legacy        *bool `json:"opencodeLaunch"`
	}
	if err := json.Unmarshal(data, &wire); err != nil {
		return err
	}
	*c = HostCaps(wire.plain)
	switch {
	case wire.SessionLaunch != nil:
		c.SessionLaunch = *wire.SessionLaunch
	case wire.Legacy != nil:
		c.SessionLaunch = *wire.Legacy
	}
	return nil
}

// LegacyHostCaps is the remote wire form of HostCaps: it repeats
// SessionLaunch as "opencodeLaunch" for hubs that predate the rename.
type LegacyHostCaps struct {
	HostCaps
	Legacy bool `json:"opencodeLaunch"`
}

// WithLegacyFields returns the remote wire form of c.
func (c HostCaps) WithLegacyFields() LegacyHostCaps {
	return LegacyHostCaps{HostCaps: c, Legacy: c.SessionLaunch}
}
