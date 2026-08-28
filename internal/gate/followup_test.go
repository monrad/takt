package gate_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/monrad/takt/internal/gate"
)

// Numbers the identity tests carry so the tables read as data: a base
// follow-up's wave, task and line, and the values each is mutated to.
const (
	waveZero  = 0
	waveOne   = 1
	waveTwo   = 2
	taskTwo   = 2
	taskThree = 3
	lineFour  = 4
	lineFive  = 5
)

func TestFollowUpsAppendAndRead(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	empty, err := gate.ReadFollowUps(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(empty.Items) != 0 {
		t.Fatalf("an absent file must read as no follow-ups, got %d", len(empty.Items))
	}
	first := gate.FollowUp{Gate: gate.Spec, Severity: "minor", File: "spec.md", Line: 42,
		Title: "wording", Detail: "ambiguous", Source: gate.SourceApprove, TS: time.Now()}
	if err = gate.AppendFollowUps(dir, first); err != nil {
		t.Fatal(err)
	}
	second := gate.FollowUp{Gate: gate.Plan, Severity: "nit", Title: "typo",
		Source: gate.SourceOverride, TS: time.Now()}
	if err = gate.AppendFollowUps(dir, second); err != nil {
		t.Fatal(err)
	}
	got, err := gate.ReadFollowUps(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Items) != 2 {
		t.Fatalf("appends must accumulate, got %d", len(got.Items))
	}
	if got.Items[0].Line != 42 || got.Items[0].Source != gate.SourceApprove {
		t.Fatalf("first item lost detail: %+v", got.Items[0])
	}
	if got.Items[1].Gate != gate.Plan {
		t.Fatalf("second item lost its gate: %+v", got.Items[1])
	}
}

func TestAppendNoFollowUpsWritesNothing(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	if err := gate.AppendFollowUps(dir); err != nil {
		t.Fatal(err)
	}
	got, err := gate.ReadFollowUps(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Items) != 0 {
		t.Fatal("appending nothing must not create the file")
	}
}

func TestFollowUpCarriesWaveTaskAndInternalSource(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	err := gate.AppendFollowUps(dir, gate.FollowUp{
		Severity: "major", File: "a.go", Line: lineFour, Title: "x",
		Source: gate.SourceInternal, Wave: new(waveTwo), Task: taskThree,
	})
	if err != nil {
		t.Fatal(err)
	}
	f, err := gate.ReadFollowUps(dir)
	if err != nil || len(f.Items) != 1 {
		t.Fatalf("read: %+v, %v", f, err)
	}
	it := f.Items[0]
	if it.Source != "internal" || it.Wave == nil || *it.Wave != waveTwo || it.Task != taskThree ||
		it.Gate != "" {
		t.Fatalf("item = %+v", it)
	}
}

// TestFollowUpWaveZeroRoundTrips pins #53: Wave is a pointer so that the
// first wave a run dispatches — wave 0 — is written as "wave": 0 and read
// back as a wave-0 follow-up, while a gate follow-up still writes no wave
// key at all. With an int and omitempty the two were indistinguishable on
// disk, and the retro rendered a wave-0 finding as if a gate had raised it.
func TestFollowUpWaveZeroRoundTrips(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	waveItem := gate.FollowUp{
		Severity: "major", File: "a.go", Line: lineFour, Title: "wave zero",
		Source: gate.SourceInternal, Wave: new(waveZero), Task: taskTwo, TS: time.Now(),
	}
	gateItem := gate.FollowUp{
		Gate: gate.Spec, Severity: "minor", Title: "gate one",
		Source: gate.SourceApprove, TS: time.Now(),
	}
	if err := gate.AppendFollowUps(dir, waveItem, gateItem); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(dir, "follow-ups.json"))
	if err != nil {
		t.Fatal(err)
	}
	var raw struct {
		Items []map[string]any `json:"items"`
	}
	if err = json.Unmarshal(b, &raw); err != nil {
		t.Fatal(err)
	}
	if len(raw.Items) != 2 {
		t.Fatalf("two items expected on disk, got %d: %s", len(raw.Items), b)
	}
	w, ok := raw.Items[0]["wave"]
	if !ok || w != float64(waveZero) {
		t.Fatalf(`a wave-0 follow-up must serialise "wave": 0, got %v (present %v): %s`, w, ok, b)
	}
	if _, ok = raw.Items[1]["wave"]; ok {
		t.Fatalf("a gate follow-up must carry no wave key: %s", b)
	}
	got, err := gate.ReadFollowUps(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got.Items[0].Wave == nil || *got.Items[0].Wave != waveZero {
		t.Fatalf("wave-0 follow-up read back as %+v", got.Items[0])
	}
	if got.Items[1].Wave != nil {
		t.Fatalf("gate follow-up read back with a wave: %+v", got.Items[1])
	}
}

// TestAppendFollowUpsIsIdempotent pins #44: a carry that runs twice — a
// retried review, a replayed close — must leave the file as one carry
// would, whether the repeat arrives in a second call or twice inside one.
func TestAppendFollowUpsIsIdempotent(t *testing.T) {
	t.Parallel()
	item := gate.FollowUp{
		Gate: gate.Spec, Severity: "minor", File: "spec.md", Line: lineFour,
		Title: "wording", Detail: "ambiguous", Source: gate.SourceApprove, TS: time.Now(),
	}
	t.Run("across calls", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		if err := gate.AppendFollowUps(dir, item); err != nil {
			t.Fatal(err)
		}
		if err := gate.AppendFollowUps(dir, item); err != nil {
			t.Fatal(err)
		}
		got, err := gate.ReadFollowUps(dir)
		if err != nil {
			t.Fatal(err)
		}
		if len(got.Items) != 1 {
			t.Fatalf("a repeated carry must not append, got %d: %+v", len(got.Items), got.Items)
		}
	})
	t.Run("within one call", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		if err := gate.AppendFollowUps(dir, item, item); err != nil {
			t.Fatal(err)
		}
		got, err := gate.ReadFollowUps(dir)
		if err != nil {
			t.Fatal(err)
		}
		if len(got.Items) != 1 {
			t.Fatalf("a duplicate inside one call must collapse, got %d: %+v", len(got.Items), got.Items)
		}
	})
}

// appendEachAndReadOne carries every item in its own call — the shape two
// carries actually have — and returns the single follow-up the file must
// hold afterwards.
func appendEachAndReadOne(t *testing.T, dir string, items ...gate.FollowUp) gate.FollowUp {
	t.Helper()
	for _, it := range items {
		if err := gate.AppendFollowUps(dir, it); err != nil {
			t.Fatal(err)
		}
	}
	got, err := gate.ReadFollowUps(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Items) != 1 {
		t.Fatalf("exactly one item expected, got %d: %+v", len(got.Items), got.Items)
	}
	return got.Items[0]
}

// TestAppendFollowUpsUpgradesApproveToOverride pins the one exception to
// "nothing stored is ever rewritten": a finding a pass closed over that the
// user then declined becomes an override in place, keeping its ts. The
// reverse is not a downgrade — an override stays an override.
func TestAppendFollowUpsUpgradesApproveToOverride(t *testing.T) {
	t.Parallel()
	base := gate.FollowUp{
		Gate: gate.Spec, Severity: "major", File: "spec.md", Line: lineFour,
		Title: "unclear", Detail: "d", TS: time.Now(),
	}
	later := base.TS.Add(time.Hour)
	t.Run("approve then override upgrades", func(t *testing.T) {
		t.Parallel()
		approve, override := base, base
		approve.Source = gate.SourceApprove
		override.Source, override.TS = gate.SourceOverride, later
		got := appendEachAndReadOne(t, t.TempDir(), approve, override)
		if got.Source != gate.SourceOverride {
			t.Errorf("source must be upgraded to override: %+v", got)
		}
		if !got.TS.Equal(approve.TS) {
			t.Errorf("the stored ts must be kept, got %v want %v", got.TS, approve.TS)
		}
	})
	t.Run("override then approve stays override", func(t *testing.T) {
		t.Parallel()
		override, approve := base, base
		override.Source = gate.SourceOverride
		approve.Source, approve.TS = gate.SourceApprove, later
		got := appendEachAndReadOne(t, t.TempDir(), override, approve)
		if got.Source != gate.SourceOverride {
			t.Errorf("an override must not be downgraded: %+v", got)
		}
		if !got.TS.Equal(override.TS) {
			t.Errorf("the stored ts must be kept, got %v want %v", got.TS, override.TS)
		}
	})
}

// TestFollowUpKeyIsInjective pins the identity #44 settled on: the seven
// elements [gate, wave, task, severity, file, line, title], trimmed, and
// nothing else. One row per element mutates exactly that element and must
// change the key — an implementation that drops any of the seven fails
// here — while the trimming and the fields outside the identity must not.
// The delimiter rows are why the key is JSON: a "|" or a quote inside a
// file name or a title must not let two different findings collide.
func TestFollowUpKeyIsInjective(t *testing.T) {
	t.Parallel()
	base := gate.FollowUp{
		Gate: gate.Spec, Wave: new(waveOne), Task: taskTwo, Severity: "major",
		File: "a.go", Line: lineFour, Title: "t", Detail: "d", Source: gate.SourceApprove,
	}
	with := func(mutate func(*gate.FollowUp)) gate.FollowUp {
		it := base
		mutate(&it)
		return it
	}
	// Every element of the identity, one row each. The wave rows cover all
	// three shapes the pointer has — a wave, no wave, and wave 0 — so the
	// pairwise check below also settles that nil and 0 are distinct.
	differ := []struct {
		name string
		item gate.FollowUp
	}{
		{"gate", with(func(f *gate.FollowUp) { f.Gate = gate.Plan })},
		{"wave nil", with(func(f *gate.FollowUp) { f.Wave = nil })},
		{"wave zero", with(func(f *gate.FollowUp) { f.Wave = new(waveZero) })},
		{"task", with(func(f *gate.FollowUp) { f.Task = taskThree })},
		{"severity", with(func(f *gate.FollowUp) { f.Severity = "minor" })},
		{"file", with(func(f *gate.FollowUp) { f.File = "b.go" })},
		{"line", with(func(f *gate.FollowUp) { f.Line = lineFive })},
		{"title", with(func(f *gate.FollowUp) { f.Title = "u" })},
	}
	seen := map[string]string{base.Key(): "base"}
	for _, c := range differ {
		k := c.item.Key()
		if k == base.Key() {
			t.Errorf("mutating %s must change the key, both are %s", c.name, k)
		}
		if other, dup := seen[k]; dup {
			t.Errorf("%s and %s share the key %s", c.name, other, k)
		}
		seen[k] = c.name
	}
	// The identity is trimmed, and Detail, Source and TS are outside it.
	same := []struct {
		name string
		item gate.FollowUp
	}{
		{"padded title", with(func(f *gate.FollowUp) { f.Title = "  t " })},
		{"padded file", with(func(f *gate.FollowUp) { f.File = " a.go" })},
		{"padded gate", with(func(f *gate.FollowUp) { f.Gate = " " + gate.Spec + " " })},
		{"padded severity", with(func(f *gate.FollowUp) { f.Severity = "major " })},
		{"other detail", with(func(f *gate.FollowUp) { f.Detail = "something else" })},
		{"other source", with(func(f *gate.FollowUp) { f.Source = gate.SourceOverride })},
		{"other ts", with(func(f *gate.FollowUp) { f.TS = time.Now() })},
	}
	for _, c := range same {
		if k := c.item.Key(); k != base.Key() {
			t.Errorf("%s must keep the key %s, got %s", c.name, base.Key(), k)
		}
	}
	// A delimiter-joined key would collide on these; a JSON array does not.
	pipeInFile := with(func(f *gate.FollowUp) { f.File, f.Title = "a|b", "c" })
	pipeInTitle := with(func(f *gate.FollowUp) { f.File, f.Title = "a", "b|c" })
	if pipeInFile.Key() == pipeInTitle.Key() {
		t.Errorf("a pipe must not move between file and title: %s", pipeInFile.Key())
	}
	quoted := with(func(f *gate.FollowUp) { f.Title = `t","u` })
	plain := with(func(f *gate.FollowUp) { f.Title = "tu" })
	if quoted.Key() == plain.Key() || quoted.Key() == base.Key() {
		t.Errorf("a quote in a title must not collide: %s", quoted.Key())
	}
}
