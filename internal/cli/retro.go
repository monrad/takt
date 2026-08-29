package cli

import (
	"os"
	"path/filepath"
	"slices"

	"github.com/monrad/takt/internal/brief"
	"github.com/monrad/takt/internal/bundle"
	"github.com/monrad/takt/internal/finish"
	"github.com/monrad/takt/internal/gate"
	"github.com/monrad/takt/internal/op"
	"github.com/monrad/takt/internal/spec"
	"github.com/monrad/takt/internal/wave"
)

// retroRunOp is the whole of the retro step: it re-derives the two finish
// artifacts the retrospective is written from and hands back the `run` op
// naming them, its instructions rendered.
//
// Because it is the whole of the step, `takt next` and `takt retro
// --rewrite` share it entire and neither derives anything for itself — the
// pair is written once per command and never twice. Everything it writes is
// a pure function of what is on disk, so a replayed call writes the same
// bytes and re-emitting the op is free (spec §5.4, §7).
func retroRunOp(o op.Op, bdir string, st *bundle.State) (op.Op, error) {
	if err := writeRetroArtifacts(bdir, st); err != nil {
		return o, err
	}
	data := brief.RunData{
		Slug: st.Slug, Topic: st.Topic,
		SpecPath: filepath.Join(bdir, "spec.md"), GoalsPath: filepath.Join(bdir, "goals.md"),
		Branch: st.Branch, Base: st.Base,
		RetroPath: filepath.Join(bdir, "retro.md"), InputsPath: finish.RetroInputsPath(bdir),
		SkeletonPath: finish.SkeletonPath(bdir),
	}
	text, err := brief.Render("run-"+op.StepRetro, data)
	if err != nil {
		return o, err
	}
	o.Instructions = text
	o.Inputs = map[string]any{
		keySlug: data.Slug, "topic": data.Topic,
		"spec_path": data.SpecPath, "goals_path": data.GoalsPath,
		"inputs_path": data.InputsPath, "retro_path": data.RetroPath,
		"skeleton_path": data.SkeletonPath,
	}
	return o, nil
}

// The retro's two finish artifacts — finish/retro-inputs.json and the
// skeleton rendered from them, finish/retro-skeleton.md — are re-derived
// here from the run's own records, so the retro op always names two files
// that describe the run as it stands (spec §7.5 step 3).
//
// One code path writes the pair, from one derivation, because the skeleton
// is a rendering of those very inputs: derived apart they could disagree.
// Both are content-reproducible and both land atomically, which makes each
// file a snapshot on its own; making the *pair* one is the run lock's job,
// not [bundle.WriteFileAtomic]'s (spec §4, §7).
//
// spec.md is read here rather than best-effort: a run that has reached the
// finish phase has one, so a read that fails is a broken bundle and not an
// assumptions table that happens to be missing — that case is the empty
// slice [spec.ParseAssumptions] returns.
func writeRetroArtifacts(bdir string, st *bundle.State) error {
	idx, err := readIndex(bdir)
	if err != nil {
		return err
	}
	events, err := bundle.ReadEvents(bdir)
	if err != nil {
		return err
	}
	closes, err := readCloses(bdir, st.Tasks)
	if err != nil {
		return err
	}
	v, err := finish.ReadVerify(bdir)
	if err != nil {
		return err
	}
	g, err := finish.ReadGoals(bdir)
	if err != nil {
		return err
	}
	fu, err := gate.ReadFollowUps(bdir)
	if err != nil {
		return err
	}
	var internals []wave.InternalRecord
	for _, n := range waveNumbers(st.Tasks) {
		recs, ierr := wave.AllInternalRecords(bdir, n)
		if ierr != nil {
			return ierr
		}
		internals = append(internals, recs...)
	}
	b, err := os.ReadFile(filepath.Join(bdir, "spec.md"))
	if err != nil {
		return err
	}
	in := finish.BuildRetroInputs(st, idx, events, closes, v, g, fu.Items, internals)
	ex := finish.SkeletonExtras{
		Shipped:   finish.BuildShipped(events, idx),
		Decisions: finish.BuildDecisions(events, st, spec.ParseAssumptions(b)),
	}
	if err = finish.WriteRetroInputs(bdir, in); err != nil {
		return err
	}
	return finish.WriteSkeleton(bdir, finish.RenderSkeleton(in, ex))
}

// waveNumbers is every wave number the run has tasks in, ascending and
// deduplicated — the wave list readCloses and the internal review gathering
// both walk.
func waveNumbers(tasks []bundle.Task) []int {
	var waves []int
	for _, t := range tasks {
		if !slices.Contains(waves, t.Wave) {
			waves = append(waves, t.Wave)
		}
	}
	slices.Sort(waves)
	return waves
}

// readCloses collects every slice record of every wave the run has tasks in,
// in wave then slice order; a wave that never wrote one is skipped rather
// than reported, because a run can reach finish with a wave whose tasks were
// all waived. A sliced wave contributes one record per slice, and the retro
// wants all of them: each slice graded different tasks.
func readCloses(bdir string, tasks []bundle.Task) ([]wave.CloseResult, error) {
	out := make([]wave.CloseResult, 0, len(tasks))
	for _, n := range waveNumbers(tasks) {
		all, err := wave.AllCloses(bdir, n)
		if err != nil {
			return nil, err
		}
		out = append(out, all...)
	}
	return out, nil
}
