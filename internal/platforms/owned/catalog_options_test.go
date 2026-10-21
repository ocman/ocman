package owned

import (
	"context"
	"reflect"
	"testing"

	"github.com/NoUseFreak/ocman/internal/platforms"
)

type optionsNative struct {
	*nativePlatform
	directories []string
}

func (p *optionsNative) AgentOptions(_ context.Context, dirs []string) []string {
	p.directories = dirs
	return []string{"agent"}
}
func (p *optionsNative) ModelOptions(context.Context) []string { return []string{"provider/model"} }

func TestCanonicalCompositionPreservesConfigurationCatalogues(t *testing.T) {
	a, p, _ := fixture(t)
	source := &optionsNative{nativePlatform: p}
	a.native = source
	var composed platforms.Platform = a
	if got := composed.(platforms.AgentOptionsReader).AgentOptions(t.Context(), []string{"/repo"}); !reflect.DeepEqual(got, []string{"agent"}) || !reflect.DeepEqual(source.directories, []string{"/repo"}) {
		t.Fatalf("%v %v", got, source.directories)
	}
	if got := composed.(platforms.ModelOptionsReader).ModelOptions(t.Context()); !reflect.DeepEqual(got, []string{"provider/model"}) {
		t.Fatal(got)
	}
	if _, ok := composed.(interface {
		StartSessionCache(context.Context, func(string) bool)
	}); ok {
		t.Fatal("canonical adapter still starts a native browsing cache")
	}
	a.native = p
	if got := a.AgentOptions(t.Context(), nil); got != nil {
		t.Fatal(got)
	}
	if got := a.ModelOptions(t.Context()); got != nil {
		t.Fatal(got)
	}
}
