package finish

import (
	"cmp"
	"encoding/json"
	"fmt"
	"maps"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/monrad/takt/internal/bundle"
	"github.com/monrad/takt/internal/gate"
	"github.com/monrad/takt/internal/plan"
	"github.com/monrad/takt/internal/spec"
)

// ShippedTask is one task of a commit row, already resolved. Title is empty
// when the plan index does not know the id — a task that committed under an
// index the run later replaced — and the row then renders the bare id, which
// is still true, rather than dropping a task that shipped.
type ShippedTask struct {
	ID    int
	Title string
}

// ShippedRow is one wave_committed event: one real commit on the branch,
// with the tasks it carried and the SHA it landed as.
type ShippedRow struct {
	Wave    int
	Slice   int
	Attempt int
	SHA     string
	Tasks   []ShippedTask
}

// Decision is one line of the retro's Decisions section: a choice a reader
// returns to months later, with the reason that was given for it. Subject
// and Choice are empty where the kind has none — a waiver names a subject
// and no choice, the disposition a choice and no subject.
type Decision struct {
	Kind    string
	Subject string
	Choice  string
	Reason  string
}

// The kinds a [Decision] can have, one per source [BuildDecisions] reads
// (spec §4).
const (
	DecisionGate           = "gate"
	DecisionTaskWaiver     = "task_waiver"
	DecisionGoalWaiver     = "goal_waiver"
	DecisionDisposition    = "disposition"
	DecisionSpecAssumption = "spec_assumption"
)

// SkeletonExtras is what the skeleton needs that [RetroInputs] does not
// carry. Both fields are already resolved: [RenderSkeleton] looks nothing
// up. There is deliberately no assumptions field — an assumption reaches the
// page only as a Decision that [BuildDecisions] made, so there is one path
// to the page and no way to render one twice (spec §4).
type SkeletonExtras struct {
	Shipped   []ShippedRow
	Decisions []Decision
}

// The event types the skeleton reads and the data keys they carry beyond the
// dispatch key retro.go already names.
const (
	evGateAnswered = "gate_answered"
	evTaskWaived   = "task_waived"
	evGoalWaived   = "goal_waived"

	keySHA    = "sha"
	keyTasks  = "tasks"
	keyGate   = "gate"
	keyChoice = "choice"
	keyReason = "reason"
	keyTask   = "task"
	keyGoal   = "goal"
)

// sourceUserConfirmed is the assumptions-table source the retro renders: a
// decision the user locked, as opposed to one the planning session assumed.
const sourceUserConfirmed = "user-confirmed"

// BuildShipped is pure: the event log plus the plan index → one row per
// wave_committed event, ordered by wave, then slice, then attempt.
//
// Every commit gets a row — the backfilled events a healed finish writes as
// well as the second commit of a wave that committed, was reworked and
// committed again. Each is a real commit on the branch, and hiding one would
// make the table claim a history that did not happen (spec §4).
//
// This is the one place [plan.Index] is read: each id is resolved to its
// title here, so [RenderSkeleton] performs no lookup and stays pure. An id
// the index does not know keeps an empty title.
func BuildShipped(events []bundle.Event, idx plan.Index) []ShippedRow {
	out := []ShippedRow{}
	for _, e := range events {
		if e.Type != evCommitted {
			continue
		}
		// timingKeyOf floors the slice to 1, so a commit written before
		// slices were recorded lands in the same column as the healed
		// events beside it rather than in a slice 0 that never existed.
		k := timingKeyOf(e)
		sha, _ := e.Data[keySHA].(string)
		out = append(out, ShippedRow{
			Wave: k.wave, Slice: k.slice, Attempt: k.attempt,
			SHA: sha, Tasks: shippedTasks(e.Data[keyTasks], idx),
		})
	}
	slices.SortStableFunc(out, func(a, b ShippedRow) int {
		return cmp.Or(
			cmp.Compare(a.Wave, b.Wave), cmp.Compare(a.Slice, b.Slice), cmp.Compare(a.Attempt, b.Attempt))
	})
	return out
}

// shippedTasks resolves one event's task ids against the index. The ids
// arrive from events.jsonl, where every number decodes as a float64, so
// anything else in the list is not an id this run wrote and is skipped.
func shippedTasks(v any, idx plan.Index) []ShippedTask {
	raw, ok := v.([]any)
	if !ok {
		return nil
	}
	out := make([]ShippedTask, 0, len(raw))
	for _, item := range raw {
		n, isNum := item.(float64)
		if !isNum {
			continue
		}
		st := ShippedTask{ID: int(n)}
		if t := idx.Task(st.ID); t != nil {
			st.Title = t.Title
		}
		out = append(out, st)
	}
	return out
}

// BuildDecisions is pure: the event log, the state and the spec's
// assumptions → the Decisions section's lines, in source order (spec §4).
//
// A gate_answered event contributes a decision only when it carries a
// non-empty reason. A reasonless answer is not a decision — gate_review →
// revise is process, not a choice a reader needs — and an event written
// before the reason was recorded carries none at all, so legacy events fall
// under the same rule rather than under an exception to it.
//
// The disposition contributes only when it exists. On the first pass it
// never does: decideFinish emits the retro before branch_finish is asked, so
// the disposition reaches the page only through a later rewrite, and
// [RenderSkeleton] says "not yet chosen" until it does. A nil state is read
// as one that decided nothing.
//
// This is the only consumer of the parsed assumptions: the user-confirmed
// rows become spec_assumption decisions and the rest are dropped.
func BuildDecisions(events []bundle.Event, st *bundle.State, as []spec.Assumption) []Decision {
	gates, taskWaivers, goalWaivers := []Decision{}, []Decision{}, []Decision{}
	for _, e := range events {
		reason, _ := e.Data[keyReason].(string)
		switch e.Type {
		case evGateAnswered:
			if reason == "" {
				continue
			}
			id, _ := e.Data[keyGate].(string)
			choice, _ := e.Data[keyChoice].(string)
			gates = append(gates, Decision{Kind: DecisionGate, Subject: id, Choice: choice, Reason: reason})
		case evTaskWaived:
			n, _ := e.Data[keyTask].(float64)
			taskWaivers = append(taskWaivers, Decision{
				Kind: DecisionTaskWaiver, Subject: fmt.Sprintf("task %d", int(n)), Reason: reason,
			})
		case evGoalWaived:
			id, _ := e.Data[keyGoal].(string)
			goalWaivers = append(goalWaivers, Decision{Kind: DecisionGoalWaiver, Subject: id, Reason: reason})
		}
	}
	out := slices.Concat(gates, taskWaivers, goalWaivers)
	if st != nil && st.Disposition != nil {
		out = append(out, Decision{
			Kind: DecisionDisposition, Choice: st.Disposition.Choice, Reason: st.Disposition.Reason,
		})
	}
	for _, a := range as {
		if a.Source != sourceUserConfirmed {
			continue
		}
		out = append(out, Decision{
			Kind: DecisionSpecAssumption, Subject: a.Question, Choice: a.Decision, Reason: a.Rationale,
		})
	}
	return out
}

// The seven headings, in the order the document renders them.
// internal/brief/templates/run-retro.md names the same strings: the session
// fills the prose slots of this document, so the two must agree byte for
// byte (spec §3).
const (
	headingShipped   = "## What shipped"
	headingDecisions = "## Decisions"
	headingWentWell  = "## What went well / what was hard"
	headingNotProven = "## Not proven"
	headingLessons   = "## Lessons"
	headingFollowUps = "## Follow-ups"
	headingNumbers   = "## Numbers"
)

// The prose slots the session fills. They are HTML comments: invisible in
// rendered markdown, greppable, and unambiguous for the check that a retro
// was written rather than copied (spec §11).
const (
	proseShipped   = "<!-- prose: what shipped — two or three sentences -->"
	proseWentWell  = "<!-- prose: what went well / what was hard — the session's own account of driving this run -->"
	proseNotProven = "<!-- prose: not proven — what else must a reader not assume is true -->"
	proseLessons   = "<!-- prose: lessons — for the next run in this repository -->"
)

// none is what a section with nothing to report renders instead of leaving
// its heading bare.
const none = "none"

// RenderSkeleton is pure: inputs + extras → the markdown of
// finish/retro-skeleton.md. Nothing here reads the filesystem, the clock or
// the plan index — the extras arrive resolved — so the same input renders
// the same bytes, a replayed next writes the file it wrote before and
// re-emitting the retro op is free (design §5.4).
//
// Every one of the seven sections renders its heading, always: a section
// with nothing to report carries an explicit "none" line and a prose-only
// section carries its slot, so no heading is ever bare — a bare heading
// reads as an omission rather than as a fact (spec §4).
func RenderSkeleton(in RetroInputs, ex SkeletonExtras) string {
	var b strings.Builder
	b.WriteString("# Retro — " + oneLine(in.Slug) + "\n")
	renderShipped(&b, ex.Shipped, sliceColumn(ex.Shipped, in.WaveTimings))
	renderDecisions(&b, ex.Decisions)
	renderProseOnly(&b, headingWentWell, proseWentWell)
	renderNotProven(&b, in)
	renderProseOnly(&b, headingLessons, proseLessons)
	renderFollowUps(&b, in.FollowUps)
	renderNumbers(&b, in)
	return b.String()
}

// heading opens a section: a blank line, the heading, a blank line.
func heading(b *strings.Builder, h string) {
	b.WriteString("\n" + h + "\n\n")
}

// renderProseOnly renders a section whose whole body is the session's: the
// slot is its content, so it never says "none".
func renderProseOnly(b *strings.Builder, h, prose string) {
	heading(b, h)
	b.WriteString(prose + "\n")
}

// lineFolder folds every line break a free-text value may carry onto the
// line it is rendered on. A reason, a title or a detail is whatever a person
// typed or a reviewing agent wrote, and a break inside one would split a
// bullet in two — or, with a "## " behind it, forge an eighth heading in a
// document whose seven headings are the contract with the template.
var lineFolder = strings.NewReplacer("\r\n", " ", "\n", " ", "\r", " ")

// oneLine is that fold. Every free-text value the renderer places on a line
// of markdown goes through it or through [cell], which folds too.
func oneLine(s string) string { return lineFolder.Replace(s) }

// cellEscaper is [lineFolder] plus the escape a table cell also needs: an
// unescaped pipe would end the cell early, and a task title is whatever the
// planner wrote.
var cellEscaper = strings.NewReplacer("|", `\|`, "\r\n", " ", "\n", " ", "\r", " ")

func cell(s string) string { return cellEscaper.Replace(s) }

// renderShipped renders the prose slot and, under it, one table row per
// commit.
func renderShipped(b *strings.Builder, rows []ShippedRow, withSlice bool) {
	heading(b, headingShipped)
	b.WriteString(proseShipped + "\n\n")
	if len(rows) == 0 {
		b.WriteString(none + "\n")
		return
	}
	shippedTable(b, rows, withSlice)
}

// shippedTable writes the wave × tasks × commit table.
func shippedTable(b *strings.Builder, rows []ShippedRow, withSlice bool) {
	head := []string{"wave"}
	if withSlice {
		head = append(head, "slice")
	}
	head = append(head, "attempt", "tasks", "commit")
	b.WriteString("| " + strings.Join(head, " | ") + " |\n")
	b.WriteString("|" + strings.Repeat(" --- |", len(head)) + "\n")
	for _, r := range rows {
		cells := []string{strconv.Itoa(r.Wave)}
		if withSlice {
			cells = append(cells, strconv.Itoa(r.Slice))
		}
		cells = append(cells, strconv.Itoa(r.Attempt), tasksCell(r.Tasks), cell(r.SHA))
		b.WriteString("| " + strings.Join(cells, " | ") + " |\n")
	}
}

// sliceColumn reports whether the table carries a slice column. A wave is
// split into slices only when it holds more tasks than max_parallel runs at
// once; on every other run the column is a column of ones that says nothing.
//
// The commits alone cannot answer the question, because a slice can close
// with nothing to commit and so leave no row: a wave whose first slice
// committed nothing would show a lone row numbered 2 with no column to say
// what the 2 counts, and a wave whose second slice committed nothing would
// be invisible in the rows entirely. The timings — one per attempt that
// closed, committing or not — are read alongside them, and since slices are
// numbered from one, any number above it is a second slice by itself.
func sliceColumn(rows []ShippedRow, timings []WaveTiming) bool {
	seen := map[int]int{}
	for _, k := range sliceKeys(rows, timings) {
		if k.slice > 1 {
			return true
		}
		if s, ok := seen[k.wave]; ok && s != k.slice {
			return true
		}
		seen[k.wave] = k.slice
	}
	return false
}

// sliceKeys is every wave-and-slice pair the run left evidence of: one per
// commit and one per attempt that closed.
func sliceKeys(rows []ShippedRow, timings []WaveTiming) []timingKey {
	out := make([]timingKey, 0, len(rows)+len(timings))
	for _, r := range rows {
		out = append(out, timingKey{wave: r.Wave, slice: r.Slice})
	}
	for _, t := range timings {
		out = append(out, timingKey{wave: t.Wave, slice: t.Slice})
	}
	return out
}

// tasksCell renders one row's tasks: "<id> — <title>", or the bare id for a
// task the index did not know.
func tasksCell(ts []ShippedTask) string {
	if len(ts) == 0 {
		return "—"
	}
	parts := make([]string, 0, len(ts))
	for _, t := range ts {
		if t.Title == "" {
			parts = append(parts, strconv.Itoa(t.ID))
			continue
		}
		parts = append(parts, strconv.Itoa(t.ID)+" — "+cell(t.Title))
	}
	return strings.Join(parts, "; ")
}

// notYetChosen is what the Decisions section says while the disposition does
// not exist — which, on the first pass, it never does: decideFinish emits
// the retro one row before branch_finish is asked. A reader then sees an
// unanswered question rather than a missing one (spec §4).
const notYetChosen = DecisionDisposition + ": not yet chosen"

// noReason stands in for a decision that carries none. The kind and the
// reason are what the section exists for, so the reason keeps its place even
// when it is empty: an answer given without one — the disposition's reason
// is optional — is a fact about the record, not a gap to hide.
const noReason = "no reason given"

// renderDecisions renders one bullet per decision, and the "not yet chosen"
// line where the disposition would be when it is not among them.
//
// That place is [BuildDecisions]'s own source order — after the waivers,
// before the spec's assumptions — so the section does not reshuffle itself
// the moment a rewrite fills the line in. The line is not a bullet, and a
// bare line between two bullets is read as a continuation of the one above
// it, so it is set off as its own paragraph wherever a bullet abuts it.
func renderDecisions(b *strings.Builder, ds []Decision) {
	heading(b, headingDecisions)
	if slices.ContainsFunc(ds, func(d Decision) bool { return d.Kind == DecisionDisposition }) {
		for _, d := range ds {
			b.WriteString(decisionLine(d) + "\n")
		}
		return
	}
	at := slices.IndexFunc(ds, func(d Decision) bool { return d.Kind == DecisionSpecAssumption })
	if at < 0 {
		at = len(ds)
	}
	for _, d := range ds[:at] {
		b.WriteString(decisionLine(d) + "\n")
	}
	if at > 0 {
		b.WriteString("\n")
	}
	b.WriteString(notYetChosen + "\n")
	if at < len(ds) {
		b.WriteString("\n")
	}
	for _, d := range ds[at:] {
		b.WriteString(decisionLine(d) + "\n")
	}
}

// decisionLine is "- <kind>: <subject> — <choice> (<reason>)", with the
// parts a kind does not carry left out and the reason always present.
func decisionLine(d Decision) string {
	parts := []string{}
	if d.Subject != "" {
		parts = append(parts, oneLine(d.Subject))
	}
	if d.Choice != "" {
		parts = append(parts, oneLine(d.Choice))
	}
	line := "- " + d.Kind
	if len(parts) > 0 {
		line += ": " + strings.Join(parts, " — ")
	}
	reason := oneLine(d.Reason)
	if reason == "" {
		reason = noReason
	}
	return line + " (" + reason + ")"
}

// renderNotProven seeds the section from the inputs alone — every task that
// did not end done, every waived goal, and a verification that was
// overridden or skipped — and leaves the prose slot for whatever else a
// reader must not assume.
func renderNotProven(b *strings.Builder, in RetroInputs) {
	heading(b, headingNotProven)
	items := notProven(in)
	if len(items) == 0 {
		b.WriteString(none + "\n")
	}
	for _, it := range items {
		b.WriteString("- " + it + "\n")
	}
	b.WriteString("\n" + proseNotProven + "\n")
}

// skippedVerification is the seed line for a run that ran no commands at
// all: nothing was proven because nothing was asked.
const skippedVerification = "verification — skipped, the run had no commands to run"

// notProven is the seed, in the order the section renders it.
func notProven(in RetroInputs) []string {
	out := []string{}
	for _, f := range in.Failures {
		out = append(out, withReason(fmt.Sprintf("task %d — %s", f.Task, oneLine(f.Status)), f.Reason))
	}
	if in.Goals != nil {
		for _, id := range slices.Sorted(maps.Keys(in.Goals.Waived)) {
			out = append(out, withReason("goal "+oneLine(id)+" — waived", in.Goals.Waived[id]))
		}
	}
	if in.Verify != nil {
		if in.Verify.Overridden != "" {
			out = append(out, withReason("verification — overridden", in.Verify.Overridden))
		}
		if in.Verify.Skipped {
			out = append(out, skippedVerification)
		}
	}
	return out
}

// withReason appends ": <reason>" when there is one.
func withReason(s, reason string) string {
	if reason == "" {
		return s
	}
	return s + ": " + oneLine(reason)
}

// severities rendered in full, in the order they are rendered.
var fullSeverities = []string{"blocking", "major"}

// severities summarised as a count, in the order they are counted. Anything
// else a reviewer wrote is counted too, under its own name, so no follow-up
// leaves the section unaccounted for.
var countedSeverities = []string{"minor", "nit"}

// renderFollowUps renders the blocking and major follow-ups in full and the
// rest as counts pointing at follow-ups.json, which holds every one verbatim
// (spec §4).
func renderFollowUps(b *strings.Builder, fs []gate.FollowUp) {
	heading(b, headingFollowUps)
	full, counts := bucketFollowUps(fs)
	for _, f := range full {
		b.WriteString(followUpLine(f) + "\n")
	}
	if counts != "" {
		b.WriteString(counts + "\n")
	}
	if len(full) == 0 && counts == "" {
		b.WriteString(none + "\n")
	}
}

// bucketFollowUps splits the follow-ups into those rendered in full, in
// severity order, and the count line summarising the others.
func bucketFollowUps(fs []gate.FollowUp) ([]gate.FollowUp, string) {
	full := []gate.FollowUp{}
	for _, sev := range fullSeverities {
		for _, f := range fs {
			if f.Severity == sev {
				full = append(full, f)
			}
		}
	}
	counts := map[string]int{}
	for _, f := range fs {
		if !slices.Contains(fullSeverities, f.Severity) {
			counts[f.Severity]++
		}
	}
	return full, countsLine(counts)
}

// countsLine names each counted severity and the file that holds those
// follow-ups verbatim. Empty when nothing was counted.
func countsLine(counts map[string]int) string {
	order := slices.Concat(countedSeverities, otherSeverities(counts))
	parts := []string{}
	for _, sev := range order {
		if n := counts[sev]; n > 0 {
			parts = append(parts, strconv.Itoa(n)+" "+oneLine(sev))
		}
	}
	if len(parts) == 0 {
		return ""
	}
	return "- " + strings.Join(parts, ", ") + " — see follow-ups.json, which holds every one verbatim"
}

// otherSeverities is the counted severities takt does not name, sorted so
// the line is deterministic.
func otherSeverities(counts map[string]int) []string {
	out := []string{}
	for sev := range counts {
		if !slices.Contains(countedSeverities, sev) {
			out = append(out, sev)
		}
	}
	slices.Sort(out)
	return out
}

// followUpLine is "- <severity> — <title> (<where>) — <detail>".
func followUpLine(f gate.FollowUp) string {
	line := "- " + oneLine(f.Severity) + " — " + oneLine(f.Title)
	if w := followUpWhere(f); w != "" {
		line += " (" + w + ")"
	}
	if f.Detail != "" {
		line += " — " + oneLine(f.Detail)
	}
	return line
}

// followUpWhere is the existing locator: a gate follow-up carries no wave,
// and a wave one names its task unless no single task owns it.
func followUpWhere(f gate.FollowUp) string {
	if f.Wave == nil {
		if f.Gate == "" {
			return ""
		}
		return "gate " + oneLine(f.Gate)
	}
	where := fmt.Sprintf("wave %d", *f.Wave)
	if f.Task != 0 {
		where += fmt.Sprintf("/task %d", f.Task)
	}
	return where
}

// numbers is the Numbers block: the two measurements #55's cross-run
// comparison reads, carried over from the inputs unchanged (spec §3).
type numbers struct {
	Internal    *InternalReview `json:"internal_review"`
	WaveTimings []WaveTiming    `json:"wave_timings"`
}

// renderNumbers fences the block, or says none when the run measured
// neither half of it.
func renderNumbers(b *strings.Builder, in RetroInputs) {
	heading(b, headingNumbers)
	if in.Internal == nil && len(in.WaveTimings) == 0 {
		b.WriteString(none + "\n")
		return
	}
	timings := in.WaveTimings
	if timings == nil {
		// A nil slice would marshal as null, which reads as "not measured"
		// beside an internal_review that was. The run measured an empty list.
		timings = []WaveTiming{}
	}
	out, err := json.MarshalIndent(numbers{Internal: in.Internal, WaveTimings: timings}, "", "  ")
	if err != nil {
		// Only the timestamps can fail this: a WaveTiming's times come from
		// the event log, and [time.Time] refuses a year outside [0,9999].
		// Saying so on one line beats opening a fence and not closing it.
		b.WriteString(oneLine("numbers could not be rendered: "+err.Error()) + "\n")
		return
	}
	b.WriteString("```json\n" + string(out) + "\n```\n")
}

// SkeletonPath is where `next` writes the skeleton the session starts its
// retro from.
func SkeletonPath(bundleDir string) string {
	return filepath.Join(bundleDir, "finish", "retro-skeleton.md")
}

// WriteSkeleton writes it atomically.
func WriteSkeleton(bundleDir, content string) error {
	return bundle.WriteFileAtomic(SkeletonPath(bundleDir), []byte(content))
}
