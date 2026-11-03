package server

import (
	"testing"

	"github.com/ocman/ocman/internal/projectsmod"
)

func TestProjectsModuleSharedInstance(t *testing.T) {
	s := &Server{}
	first := s.projectsModule()
	if s.projectsModule() != first {
		t.Fatal("projects module must be shared by registry and listing callers")
	}
}

func TestProjectsModuleUsesInjectedInstance(t *testing.T) {
	s := &Server{}
	injected := projectsmod.NewModule(projectsmod.Deps{})
	s.projectsMod.set(injected)
	if s.projectsModule() != injected {
		t.Fatal("projects module must retain the injected instance")
	}
}
