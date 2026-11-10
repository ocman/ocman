package app

import (
	"errors"
	"flag"
	"reflect"
	"strings"
	"testing"
)

func TestParsedFlagArgumentsPreservesTheRealFlagSet(t *testing.T) {
	fs := flag.NewFlagSet("arguments", flag.ContinueOnError)
	text := fs.String("value", "original", "")
	boolean := fs.Bool("enabled", false, "")
	values, err := parsedFlagArguments(fs, []string{"--value=first", "--value", "--not-a-flag", "--enabled", "--", "--value=ignored"})
	if err != nil || !reflect.DeepEqual(values, map[string]string{"value": "--not-a-flag", "enabled": "true"}) {
		t.Fatalf("parsed arguments = %v, %v", values, err)
	}
	if *text != "original" || *boolean || fs.NFlag() != 0 {
		t.Fatal("argument inspection mutated values or flag provenance")
	}
	for _, args := range [][]string{{"--unknown=private-credential"}, {"--value"}} {
		if _, err := parsedFlagArguments(fs, args); err == nil || strings.Contains(err.Error(), "private-credential") {
			t.Fatalf("unsafe argument error = %v", err)
		}
		if flagPresent(fs, args, "value") {
			t.Fatal("invalid arguments counted as a consumed flag")
		}
	}
	if _, err := parsedFlagArguments(fs, []string{"--help"}); !errors.Is(err, flag.ErrHelp) {
		t.Fatalf("help error = %v", err)
	}
	if _, err := prepareFileConfig(fs, []string{"--help"}, "unused.yaml"); err != nil {
		t.Fatal("configuration loading prevented CLI help")
	}
}
