package goals_test

import (
	"strings"
	"testing"

	"github.com/monrad/takt/internal/goals"
)

const sample = "# Goals — demo\n\n## Anchor\n```text\nfull https://github.com/x/y/issues/7 — make it\nwork across two lines\n```\n\n## Goals\n- G1 — Policy generation yields a schema-valid set · signal: test · evidence: go test ./lib/... passes\n- G2 — The decision is recorded · signal: docs · evidence: an ADR under documentation/decisions/\n"

func TestParseSample(t *testing.T) {
	t.Parallel()
	g, err := goals.Parse([]byte(sample))
	if err != nil {
		t.Fatal(err)
	}
	if g.Anchor != "full https://github.com/x/y/issues/7 — make it\nwork across two lines" {
		t.Fatalf("anchor = %q", g.Anchor)
	}
	if len(g.Items) != 2 || g.Items[0].ID != "G1" || g.Items[0].Signal != "test" ||
		!strings.HasPrefix(g.Items[1].Evidence, "an ADR") {
		t.Fatalf("items = %+v", g.Items)
	}
	if ids := g.IDs(); len(ids) != 2 || ids[1] != "G2" {
		t.Fatalf("IDs = %v", ids)
	}
}

func TestParseRejects(t *testing.T) {
	t.Parallel()
	bad := map[string]string{
		"no anchor":     strings.Replace(sample, "## Anchor", "## Intro", 1),
		"no goals":      sample[:strings.Index(sample, "## Goals")], //nolint:gocritic // sample is a fixed literal that always contains "## Goals"
		"bad signal":    strings.Replace(sample, "signal: test", "signal: vibes", 1),
		"duplicate id":  strings.Replace(sample, "- G2 —", "- G1 —", 1),
		"missing evid.": strings.Replace(sample, " · evidence: go test ./lib/... passes", "", 1),
		"gap in ids":    strings.Replace(sample, "- G2 —", "- G3 —", 1),
	}
	for name, in := range bad {
		if _, err := goals.Parse([]byte(in)); err == nil {
			t.Errorf("%s: expected an error", name)
		}
	}
}

func TestHashIsStableAndPrefixed(t *testing.T) {
	t.Parallel()
	a, b := goals.Hash([]byte(sample)), goals.Hash([]byte(sample))
	if a != b || !strings.HasPrefix(a, "sha256:") || len(a) != len("sha256:")+64 {
		t.Fatalf("hash = %q", a)
	}
	if goals.Hash([]byte(sample+"x")) == a {
		t.Fatal("different bytes must hash differently")
	}
}
