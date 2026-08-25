package plan_test

import (
	"os"
	"strings"
	"testing"

	"github.com/monrad/takt/internal/plan"
)

// loadFixture reads the shared realistic fixture. Shared across this
// package's other test files (index_test.go, waves_test.go,
// validate_test.go).
func loadFixture(t *testing.T) plan.Index {
	t.Helper()
	b, err := os.ReadFile("testdata/cedar-like.json")
	if err != nil {
		t.Fatal(err)
	}
	idx, err := plan.ParseIndex(b)
	if err != nil {
		t.Fatal(err)
	}
	return idx
}

func TestParseIndexFixture(t *testing.T) {
	t.Parallel()
	idx := loadFixture(t)
	if idx.Schema != 1 || len(idx.Tasks) != 8 || idx.Tasks[0].ID != 1 || idx.Tasks[7].Class != "docs" {
		t.Fatalf("parsed = %+v", idx)
	}
	if idx.Task(5) == nil || idx.Task(5).DependsOn[1] != 3 {
		t.Fatal("Task(5)")
	}
}

func TestParseIndexNormalisesClassAndSorts(t *testing.T) {
	t.Parallel()
	idx, err := plan.ParseIndex([]byte(`{"schema":1,"spec_hash":"x","tasks":[
	  {"id":2,"title":"b","description":"d","files":["b.go"],"verify":["true"]},
	  {"id":1,"title":"a","description":"d","files":["a.go"],"verify":["true"]}]}`))
	if err != nil {
		t.Fatal(err)
	}
	if idx.Tasks[0].ID != 1 || idx.Tasks[0].Class != "implement" || idx.Tasks[1].Class != "implement" {
		t.Fatalf("%+v", idx.Tasks)
	}
}

func TestParseIndexRejectsGarbage(t *testing.T) {
	t.Parallel()
	if _, err := plan.ParseIndex([]byte(`{"schema":1,"tasks":[{"id":"one"}]}`)); err == nil {
		t.Fatal("string id must fail")
	}
	if _, err := plan.ParseIndex([]byte(`not json`)); err == nil {
		t.Fatal("non-JSON must fail")
	}
}

func TestCanonicalStripsWaveAndIsStable(t *testing.T) {
	t.Parallel()
	idx := loadFixture(t)
	a, _ := plan.Canonical(idx)
	w := 3
	idx.Tasks[0].Wave = &w
	b, _ := plan.Canonical(idx)
	if string(a) != string(b) {
		t.Fatal("wave must not affect the canonical bytes")
	}
}

// TestParseIndexRejectsUnknownFields covers review finding 4: a typo like
// "dependsOn" used to be dropped silently, so a plan that declared an order
// validated as valid with every task in wave 0.
func TestParseIndexRejectsUnknownFields(t *testing.T) {
	t.Parallel()
	cases := map[string]string{
		"dependsOn": `{"schema":1,"spec_hash":"x","tasks":[
		  {"id":1,"title":"a","description":"d","files":["a.go"],"verify":["true"]},
		  {"id":2,"title":"b","description":"d","files":["b.go"],"verify":["true"],"dependsOn":[1]}]}`,
		"specHash":     `{"schema":1,"specHash":"x","tasks":[]}`,
		"max_parallel": `{"schema":1,"spec_hash":"x","max_parallel":4,"tasks":[]}`,
	}
	for field, raw := range cases {
		_, err := plan.ParseIndex([]byte(raw))
		if err == nil {
			t.Fatalf("%s: unknown field must be rejected", field)
		}
		if !strings.Contains(err.Error(), field) {
			t.Fatalf("%s: error must name the field: %v", field, err)
		}
	}
}

// TestParseIndexRejectsTrailingData covers review finding 4's second half:
// json.Decoder reads only the first value, so a second object or stray text
// after the index used to be ignored.
func TestParseIndexRejectsTrailingData(t *testing.T) {
	t.Parallel()
	for name, raw := range map[string]string{
		"second object": `{"schema":1,"spec_hash":"x","tasks":[]}{"schema":1}`,
		"garbage":       `{"schema":1,"spec_hash":"x","tasks":[]} oops`,
		"array":         `{"schema":1,"spec_hash":"x","tasks":[]}[1,2]`,
	} {
		if _, err := plan.ParseIndex([]byte(raw)); err == nil {
			t.Fatalf("%s: trailing data must be rejected", name)
		}
	}
}

// TestParseIndexAcceptsTrailingWhitespace keeps the strictness above from
// rejecting an ordinary file that ends with a newline.
func TestParseIndexAcceptsTrailingWhitespace(t *testing.T) {
	t.Parallel()
	if _, err := plan.ParseIndex([]byte(`{"schema":1,"spec_hash":"x","tasks":[]}` + "\n\n  \t")); err != nil {
		t.Fatalf("trailing whitespace must be accepted: %v", err)
	}
}
