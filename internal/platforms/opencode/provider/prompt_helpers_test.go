package opencode

import "github.com/NoUseFreak/ocman/internal/platforms"

func (a *Adapter) ObservePromptAsked(port, directory, kind string, prompt platforms.LivePrompt) {
	a.ObservePromptAskedFromPort(port, 0, directory, kind, prompt)
}
