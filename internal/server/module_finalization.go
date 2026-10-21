package server

import "github.com/NoUseFreak/ocman/internal/module"

func finalizeModules(mods []module.Module) {
	for i := len(mods) - 1; i >= 0; i-- {
		if f, ok := mods[i].(module.Finalizer); ok {
			f.Finalize()
		}
	}
}
