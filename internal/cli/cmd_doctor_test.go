package cli_test

import (
	"bytes"
	"strings"
	"testing"

	"github.com/monrad/takt/internal/cli"
	"github.com/monrad/takt/internal/testutil"
)

func TestDoctorTextAndExitCode(t *testing.T) {
	t.Parallel()
	root := testutil.NewRepo(t)
	runIn(t, root, nil, "init", "--slug", "demo", "topic")
	var out bytes.Buffer
	getenv := func(k string) string {
		if k == "HOME" {
			return root + "/.home"
		}
		return ""
	}
	if code := cli.Main([]string{"doctor"}, &out, &out, getenv, root); code != 0 {
		t.Fatalf("healthy repo: exit %d\n%s", code, out.String())
	}
	if !strings.Contains(out.String(), "PASS  state-schema") {
		t.Fatalf("output = %s", out.String())
	}
	testutil.WriteFile(t, root, "docs/takt/demo/plan.index.json", `{"schema":1,"spec_hash":"x","tasks":[
	  {"id":1,"title":"a","description":"d","files":["a.go"],"verify":["true"]},
	  {"id":2,"title":"b","description":"d","files":["a.go"],"verify":["true"]}]}`)
	out.Reset()
	if code := cli.Main([]string{"doctor"}, &out, &out, getenv, root); code != 1 {
		t.Fatalf("overlap: exit %d\n%s", code, out.String())
	}
	if !strings.Contains(out.String(), "ERROR plan-disjoint") || !strings.Contains(out.String(), "fix:") {
		t.Fatalf("output = %s", out.String())
	}
	code, got, _ := runIn(t, root, nil, "doctor", "--json")
	if code != 1 || got["errors"] != float64(1) {
		t.Fatalf("--json: %d %v", code, got)
	}
}
