package cli_test

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

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
	if got["version"] != version.Version {
		t.Fatalf("version = %q, want %q", got["version"], version.Version)
	}
}

func TestVersionExpectMismatchExits1(t *testing.T) {
	t.Parallel()
	code, _, errb := run(t, "version", "--expect", "9.9.9")
	if code != 1 {
		t.Fatalf("exit %d, want 1", code)
	}
	if !strings.Contains(errb, `"error"`) || !strings.Contains(errb, "9.9.9") {
		t.Fatalf("stderr = %q", errb)
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
