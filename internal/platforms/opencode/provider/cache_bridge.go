package opencode

import "github.com/ocman/ocman/internal/platforms"

func (a *Adapter) InstancePorts() platforms.InstancePorts { return a.Discovery() }
