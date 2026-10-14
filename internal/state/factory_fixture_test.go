package state

// BuiltInTracerFormula returns Factory's built-in tracer formula. The
// external test package sets it (factory_service_test.go) because this
// package's own tests must not import internal/factory: Factory's service
// depends on state, so that import would be a cycle once factorymod merges
// into factory.
var BuiltInTracerFormula func() (id string, version int, source, hash string)

type tracerFormula struct {
	ID      string
	Version int
	Source  string
	Hash    string
}

func builtInTracerFormula() tracerFormula {
	if BuiltInTracerFormula == nil {
		panic("state tests: BuiltInTracerFormula is set by the state_test package")
	}
	id, version, source, hash := BuiltInTracerFormula()
	return tracerFormula{ID: id, Version: version, Source: source, Hash: hash}
}
