package opencode

import "github.com/NoUseFreak/ocman/internal/platforms"

func (a *Adapter) InstancePorts() platforms.InstancePorts { return a.Discovery() }
