package app

import (
	"errors"
	"flag"
	"io"
)

// parsedFlagArguments records only flags consumed by Go's parser. Values are
// copied so this pass cannot mutate the actual flag set or mark file/env values
// as explicit CLI arguments. The normal parse still validates each flag value.
func parsedFlagArguments(fs *flag.FlagSet, args []string) (map[string]string, error) {
	parsed := flag.NewFlagSet(fs.Name(), flag.ContinueOnError)
	parsed.SetOutput(io.Discard)
	fs.VisitAll(func(f *flag.Flag) {
		isBool := false
		if value, ok := f.Value.(interface{ IsBoolFlag() bool }); ok {
			isBool = value.IsBoolFlag()
		}
		parsed.Var(&argumentValue{value: f.Value.String(), boolean: isBool}, f.Name, f.Usage)
	})
	if err := parsed.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil, flag.ErrHelp
		}
		// Flag errors can include the supplied value, including credentials.
		return nil, errors.New("invalid command-line flags")
	}
	values := map[string]string{}
	parsed.Visit(func(f *flag.Flag) { values[f.Name] = f.Value.String() })
	return values, nil
}

type argumentValue struct {
	value   string
	boolean bool
}

func (v *argumentValue) String() string   { return v.value }
func (v *argumentValue) IsBoolFlag() bool { return v.boolean }
func (v *argumentValue) Set(value string) error {
	v.value = value
	return nil
}

func flagPresent(fs *flag.FlagSet, args []string, name string) bool {
	values, err := parsedFlagArguments(fs, args)
	if err != nil {
		return false
	}
	_, present := values[name]
	return present
}
