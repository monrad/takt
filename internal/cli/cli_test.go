package cli_test

import (
	"bytes"
	"encoding/json"
	"flag"
	"io"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/monrad/takt/internal/cli"
	"github.com/monrad/takt/internal/version"
)

func run(t *testing.T, args ...string) (int, string, string) {
	t.Helper()
	var out, errb bytes.Buffer
	code := cli.Main(args, &out, &errb, func(string) string { return "" }, t.TempDir())
	return code, out.String(), errb.String()
}

func TestVersionPrintsJSON(t *testing.T) {
	t.Parallel()
	code, out, _ := run(t, "version")
	if code != 0 {
		t.Fatalf("exit %d", code)
	}
	var got map[string]string
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("stdout is not JSON: %q", out)
	}
	if got["version"] != version.Current() {
		t.Fatalf("version = %q, want %q", got["version"], version.Current())
	}
}

// TestVersionExpectRefusesAVersionlessExpectation replaces the strict-compare
// test this flag used to carry. `--expect` now makes the same judgment as
// `--expect-manifest`, so the 0.0.0-dev test binary matches whatever version
// a host's prompt pins (TestVersionExpectAcceptsADevBuild covers the reply's
// shape, TestManifestFailure the stamped binary's mismatch). What no build
// may match is an expectation with no version in it — a handshake line that
// lost its version is a broken host, not a match.
//
// It has to say so in the host's own terms. Routing the blank case through
// manifestFailure printed "plugin manifest --expect has no version field"
// and sent the reader to reinstall a plugin bundle `--expect` never reads:
// this flag exists for the host that carries the version in its prompt.
func TestVersionExpectRefusesAVersionlessExpectation(t *testing.T) {
	t.Parallel()
	code, _, errb := run(t, "version", "--expect", "   ")
	if code != 1 {
		t.Fatalf("exit %d, want 1", code)
	}
	if !strings.Contains(errb, "the host's handshake names no version") {
		t.Errorf("the error must name the handshake, not a manifest: %s", errb)
	}
	if strings.Contains(errb, "plugin manifest") {
		t.Errorf("--expect reads no manifest, so it must not name one: %s", errb)
	}
	if !strings.Contains(errb, "check the host prompt's takt version --expect line") {
		t.Errorf("the hint must point at the host prompt: %s", errb)
	}
}

func TestUnknownCommandExits2(t *testing.T) {
	t.Parallel()
	code, _, errb := run(t, "bogus")
	if code != 2 {
		t.Fatalf("exit %d, want 2", code)
	}
	if !strings.Contains(errb, "unknown command") {
		t.Fatalf("stderr = %q", errb)
	}
}

func TestVersionBadFlagExitsUsageWithJSON(t *testing.T) {
	t.Parallel()
	code, _, errb := run(t, "version", "--bogus")
	if code != 2 {
		t.Fatalf("exit %d, want 2", code)
	}
	var got map[string]string
	if err := json.Unmarshal([]byte(errb), &got); err != nil {
		t.Fatalf("stderr is not JSON: %q", errb)
	}
	if !strings.Contains(got["error"], "bogus") {
		t.Fatalf(`stderr "error" = %q, want it to contain "bogus"`, got["error"])
	}
}

// TestCommandContextHasDeadline covers review finding 6: every command runs
// git under a deadline (spec §13), so a hung hook fails the command instead
// of hanging takt.
func TestCommandContextHasDeadline(t *testing.T) {
	t.Parallel()
	ctx, cancel := cli.CommandContext(cli.Env{})
	defer cancel()
	deadline, ok := ctx.Deadline()
	if !ok {
		t.Fatal("commandContext must return a context with a deadline")
	}
	if d := time.Until(deadline); d <= 0 || d > 2*time.Minute {
		t.Fatalf("deadline in %v, want a positive value no greater than the 2m budget", d)
	}
}

// TestParseInterspersedOrderings covers review finding 2's helper directly:
// flags before, after, and between positionals; a literal -- ending flag
// parsing; and a bare "-" staying a positional rather than looping forever.
func TestParseInterspersedOrderings(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name  string
		args  []string
		slug  string
		want  []string
		isErr bool
	}{
		{name: "flags first", args: []string{"--slug", "d", "a", "b"}, slug: "d", want: []string{"a", "b"}},
		{name: "flags last", args: []string{"a", "b", "--slug", "d"}, slug: "d", want: []string{"a", "b"}},
		{name: "flags between", args: []string{"a", "--slug", "d", "b"}, slug: "d", want: []string{"a", "b"}},
		{name: "equals form", args: []string{"a", "--slug=d"}, slug: "d", want: []string{"a"}},
		{name: "double dash", args: []string{"--", "-x", "--slug"}, slug: "", want: []string{"-x", "--slug"}},
		{name: "bare dash", args: []string{"-", "a"}, slug: "", want: []string{"-", "a"}},
		{name: "no args", args: nil, slug: "", want: nil},
		{name: "unknown flag", args: []string{"a", "--nope"}, isErr: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			fs := flag.NewFlagSet("t", flag.ContinueOnError)
			fs.SetOutput(io.Discard)
			slug := fs.String("slug", "", "")
			got, err := cli.ParseInterspersed(fs, tc.args)
			if tc.isErr {
				if err == nil {
					t.Fatalf("want an error, got %v", got)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if *slug != tc.slug || !slices.Equal(got, tc.want) {
				t.Fatalf("positional = %v, slug = %q; want %v, %q", got, *slug, tc.want, tc.slug)
			}
		})
	}
}
