package cmd

import (
	"reflect"
	"regexp"
	"strings"
	"testing"
)

// runFlagNames collects every flag "run" accepts, from the struct tags, so the
// test below reads the same list kong does rather than a copy that can rot.
func runFlagNames() map[string]bool {
	names := map[string]bool{}
	var walk func(t reflect.Type)
	walk = func(t reflect.Type) {
		for i := 0; i < t.NumField(); i++ {
			f := t.Field(i)
			if f.Anonymous || f.Tag.Get("embed") != "" {
				if f.Type.Kind() == reflect.Struct {
					walk(f.Type)
				}
				continue
			}
			if name := f.Tag.Get("name"); name != "" {
				names[name] = true
				continue
			}
			// kong derives the flag name from the field name otherwise
			names[kebab(f.Name)] = true
		}
	}
	walk(reflect.TypeOf(RunCmd{}))
	return names
}

func kebab(name string) string {
	var b strings.Builder
	for i, r := range name {
		if r >= 'A' && r <= 'Z' {
			if i > 0 {
				b.WriteByte('-')
			}
			b.WriteRune(r - 'A' + 'a')
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

// The topic is where the reasoning behind the flags lives now, so a flag that
// gets renamed has to be renamed there too. Nothing else would catch it: the
// text is a constant, so it keeps compiling and keeps printing whatever name
// it was written with.
func TestTuningHelpNamesOnlyRealFlags(t *testing.T) {
	flags := runFlagNames()

	// --help-tuning names itself; everything else in the prose must be a flag
	elsewhere := map[string]bool{"help-tuning": true}

	seen := map[string]bool{}
	for _, m := range regexp.MustCompile(`--[a-z][a-z0-9-]*`).FindAllString(tuningHelp, -1) {
		name := strings.TrimPrefix(m, "--")
		if seen[name] || elsewhere[name] {
			continue
		}
		seen[name] = true
		if !flags[name] {
			t.Errorf("--help-tuning mentions --%s, which is not a flag of \"run\"", name)
		}
	}
	if len(seen) < 15 {
		t.Errorf("--help-tuning only names %d flags, it is meant to cover the tuning surface", len(seen))
	}
}

// Every flag that moves a statistic should be reachable from the topic --
// that is the whole point of shortening its own help down to one line.
func TestTuningHelpCoversTheTuningFlags(t *testing.T) {
	mustCover := []string{
		"rows", "no-skip-fields", "target-bytes-per-row", "target-relpages",
		"stat-file", "values-freq-map", "null-freq", "query-param-freq",
		"default-relationship", "binomial", "sequential", "normal", "pareto",
		"coin-flip-percent", "normal-stddev", "normal-mean", "pareto-s", "pareto-v",
		"query", "table", "add-fk", "no-fk-guess", "fill-fk-parents", "truncate",
	}
	for _, name := range mustCover {
		if !strings.Contains(tuningHelp, "--"+name) {
			t.Errorf("--help-tuning never mentions --%s", name)
		}
	}
}

func TestTuningHelpFlagIsAKongHook(t *testing.T) {
	// kong only runs BeforeReset on a type that declares it; if the method is
	// renamed or its signature drifts, --help-tuning silently stops working
	// and the run proceeds instead.
	var f interface{} = tuningHelpFlag(true)
	if _, ok := f.(interface{ IgnoreDefault() }); !ok {
		t.Error("tuningHelpFlag no longer declares IgnoreDefault")
	}
	if reflect.TypeOf(f).NumMethod() < 2 {
		t.Errorf("tuningHelpFlag has %d methods, expected BeforeReset and IgnoreDefault", reflect.TypeOf(f).NumMethod())
	}
}
