package cli

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/monrad/takt/internal/bundle"
	"github.com/monrad/takt/internal/finish"
	"github.com/monrad/takt/internal/gate"
	"github.com/monrad/takt/internal/goals"
	"github.com/monrad/takt/internal/wave"
)

// statusGoal is one goal as shown in status output (spec §11).
type statusGoal struct {
	ID     string
	Text   string
	Signal string
}

// taskLine is one task's status line: identity and progress, plus — once an
// implementer has reported — the attempt and model that produced its last
// digest (spec §11).
type taskLine struct {
	ID      int    `json:"id"`
	Wave    int    `json:"wave"`
	Status  string `json:"status"`
	Class   string `json:"class"`
	Attempt int    `json:"attempt"`
	Model   string `json:"model"`
}

// alignmentDigest summarises alignment.json for status: how many clauses
// landed each verdict, and which ones drifted the ask narrower (contraction:
// narrowed, dropped or contradicted) or wider (creep: widened) than what was
// confirmed (spec §7.3, §11).
type alignmentDigest struct {
	Confirmed   bool           `json:"confirmed"`
	Counts      map[string]int `json:"counts"`
	Contraction []string       `json:"contraction"`
	Creep       []string       `json:"creep"`
}

// internalStatus is the wave's internal-review line (two-layers design §5.7).
type internalStatus struct {
	LensesRecorded int  `json:"lenses_recorded"`
	LensesTotal    int  `json:"lenses_total"`
	Candidates     int  `json:"candidates"`
	Confirmed      int  `json:"confirmed"`
	VerifyPending  bool `json:"verify_pending"`
	Skipped        bool `json:"skipped"`
}

// finishStatus is the finish-phase block of status (spec §11).
type finishStatus struct {
	VerifiedSHA     string            `json:"verified_sha,omitempty"`
	VerifyPassed    *bool             `json:"verify_passed,omitempty"`
	GoalsCheckedSHA string            `json:"goals_checked_sha,omitempty"`
	Goals           map[string]string `json:"goals,omitempty"`
	Disposition     string            `json:"disposition,omitempty"`
	PRURL           string            `json:"pr_url,omitempty"`
	Applied         bool              `json:"applied"`
}

// statusInfo is a typed view of one bundle's status; statusDoc builds it and
// both statusJSON and renderStatus consume it directly, so neither renderer
// needs a type assertion on an `any`-valued map (errcheck's
// check-type-assertions is enabled repo-wide; see cmd_plan.go for the same
// pattern applied to plan.Validate's problems).
type statusInfo struct {
	Slug          string
	Phase         string
	Branch        string
	BranchAdopted bool
	Base          string
	BaseSHA       string
	TasksTotal    int
	TasksByStatus map[string]int
	Tasks         []taskLine
	Gates         map[string]string
	GatesLive     map[string]string
	ActiveWave    *bundle.ActiveWave
	PendingGate   *bundle.PendingGate
	Session       *bundle.Session
	Goals         []statusGoal
	GoalsFrozen   bool
	Alignment     *alignmentDigest
	Finish        *finishStatus
	Internal      *internalStatus
}

func cmdStatus(env Env) int {
	fs := flag.NewFlagSet("status", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	dirFlag := addDirFlag(fs)
	slug := fs.String("slug", "", "run to show")
	asJSON := fs.Bool("json", false, "print a JSON document instead of text")
	if err := fs.Parse(env.Args); err != nil {
		return usageError(env, fs, err)
	}
	info, code := loadStatus(env, *dirFlag, *slug)
	if code != 0 {
		return code
	}
	if *asJSON {
		if err := writeJSON(env.Stdout, statusJSON(info)); err != nil {
			return 1
		}
		return 0
	}
	fmt.Fprint(env.Stdout, renderStatus(info))
	return 0
}

// loadStatus resolves the workspace and the selected bundle, then builds its
// status view (spec §11).
func loadStatus(env Env, dirFlag, slugFlag string) (statusInfo, int) {
	ctx, cancel := commandContext(env)
	defer cancel()
	ws, err := openWorkspace(ctx, env, dirFlag)
	if err != nil {
		return statusInfo{}, fail(env.Stderr, 1, err.Error(), workspaceHint)
	}
	s, err := selectSlug(ws, slugFlag)
	if err != nil {
		return statusInfo{}, failSelectSlug(env, err)
	}
	bdir, st, err := loadBundle(ws, s)
	if err != nil {
		return statusInfo{}, fail(env.Stderr, 1, err.Error(), "")
	}
	return statusDoc(bdir, st), 0
}

// statusDoc builds the machine-readable status (spec §11).
func statusDoc(bdir string, st *bundle.State) statusInfo {
	info := statusInfo{
		Slug: st.Slug, Phase: st.Phase, Branch: st.Branch, BranchAdopted: st.BranchAdopted,
		Base: st.Base, BaseSHA: st.BaseSHA,
		TasksTotal: len(st.Tasks), TasksByStatus: taskCounts(st.Tasks), Tasks: statusTasks(st.Tasks),
		Gates: st.Gates, GatesLive: liveGates(bdir), ActiveWave: st.ActiveWave, PendingGate: st.PendingGate,
		Goals: []statusGoal{}, Alignment: statusAlignment(bdir), Session: readStatusSession(bdir),
	}
	if st.Phase == bundle.PhaseFinish || st.Phase == bundle.PhaseArchived {
		info.Finish = statusFinish(bdir, st)
	}
	if st.ActiveWave != nil && len(st.Config.Review.Lenses) > 0 {
		info.Internal = statusInternal(bdir, st)
	}
	b, err := os.ReadFile(filepath.Join(bdir, "goals.md"))
	if err != nil {
		return info
	}
	if g, gerr := goals.Parse(b); gerr == nil {
		info.Goals = statusGoals(g.Items)
		info.GoalsFrozen = st.GoalsHash != nil && *st.GoalsHash == goals.Hash(b)
	}
	return info
}

// statusFinish builds the finish-phase block: verify and goal verdicts read
// from their finish/ records — the run's actual recorded outcome, never
// re-derived — plus the disposition off state (spec §11). Reading is
// best-effort: a record that fails to parse is treated as absent rather
// than failing the whole status read, which must keep working on an
// archived run with no session and no writes.
func statusFinish(bdir string, st *bundle.State) *finishStatus {
	fin := &finishStatus{}
	if st.VerifiedSHA != nil {
		fin.VerifiedSHA = *st.VerifiedSHA
	}
	if st.GoalsCheckedSHA != nil {
		fin.GoalsCheckedSHA = *st.GoalsCheckedSHA
	}
	if v, err := finish.ReadVerify(bdir); err == nil && v != nil {
		fin.VerifyPassed = &v.Passed
	}
	if g, err := finish.ReadGoals(bdir); err == nil && g != nil {
		fin.Goals = goalVerdicts(g)
	}
	if st.Disposition != nil {
		fin.Disposition = st.Disposition.Choice
		fin.PRURL = st.Disposition.PRURL
		fin.Applied = st.Disposition.Applied
	}
	return fin
}

// goalVerdicts maps each goal id to its verdict, or "waived: <reason>" when
// the goal was waived instead of achieved (spec §11).
func goalVerdicts(g *finish.GoalsRecord) map[string]string {
	out := map[string]string{}
	for _, v := range g.Verdicts {
		if reason, waived := g.Waived[v.ID]; waived {
			out[v.ID] = "waived: " + reason
			continue
		}
		out[v.ID] = v.Verdict
	}
	return out
}

// statusInternal reads the internal review's state for the active dispatch —
// the same reads gatherInternalFacts does (facts.go) — but read-only and
// error-tolerant: any read error yields nil, since `takt status` must stay
// read-only and keep working even on a bundle it cannot fully parse
// (two-layers design §5.7).
func statusInternal(bdir string, st *bundle.State) *internalStatus {
	aw := st.ActiveWave
	lenses := st.Config.Review.Lenses
	in := &internalStatus{LensesTotal: len(lenses)}
	records := map[string]*wave.LensRecord{}
	for _, l := range lenses {
		r, err := wave.ReadLensRecord(bdir, aw.N, sliceOf(aw), aw.Attempt, l)
		if err != nil {
			return nil
		}
		if r != nil {
			in.LensesRecorded++
			records[l] = r
		}
	}
	allRecorded := in.LensesRecorded == in.LensesTotal
	if allRecorded {
		in.Candidates = len(wave.MergeCandidates(lenses, records))
	}
	rec, err := wave.ReadInternalRecord(bdir, aw.N, sliceOf(aw), aw.Attempt)
	if err != nil {
		return nil
	}
	switch {
	case rec != nil:
		in.Confirmed = len(rec.Confirmed)
	case allRecorded && in.Candidates > 0:
		// Zero merged candidates means `takt next` completes the internal
		// review without ever dispatching a verifier (record_reviewer.go's
		// recordVerify refuses "no candidates to verify") — so this must not
		// claim a verify is pending when none will ever be.
		in.VerifyPending = true
	}
	events, err := bundle.ReadEvents(bdir)
	if err != nil {
		return nil
	}
	in.Skipped = internalSkipped(events, aw.N, sliceOf(aw), aw.Attempt)
	return in
}

// taskCounts tallies tasks by status (spec §4.3's closed status set).
func taskCounts(tasks []bundle.Task) map[string]int {
	counts := map[string]int{
		bundle.StatusPending: 0, bundle.StatusDone: 0, bundle.StatusFailed: 0,
		bundle.StatusBlocked: 0, bundle.StatusWaived: 0,
	}
	for _, t := range tasks {
		counts[t.Status]++
	}
	return counts
}

// statusTasks builds one line per task, reading the model it last ran on off
// its last recorded digest (spec §11); "" when there is no digest yet.
func statusTasks(tasks []bundle.Task) []taskLine {
	list := make([]taskLine, 0, len(tasks))
	for _, t := range tasks {
		list = append(list, taskLine{
			ID: t.ID, Wave: t.Wave, Status: t.Status, Class: t.Class,
			Attempt: t.Attempt, Model: digestModelOf(t.LastDigest),
		})
	}
	return list
}

// digestModelOf reads the model field off a task's last digest (the JSON
// bundle.Task.LastDigest holds); "" when there is none yet or it is
// unreadable.
func digestModelOf(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var d struct {
		Model string `json:"model"`
	}
	if err := json.Unmarshal(raw, &d); err != nil {
		return ""
	}
	return d.Model
}

// liveGates computes each review gate's verdict directly from the artifacts
// and receipts on disk — never the state.gates snapshot, which only records
// that a transition once passed, not whether it still does (spec §9, §11).
// A gate whose artifacts are not all present yet is omitted.
func liveGates(bdir string) map[string]string {
	events, _ := bundle.ReadEvents(bdir)
	out := map[string]string{}
	for _, g := range []string{gate.Spec, gate.Plan} {
		st, err := gate.Compute(bdir, g, events)
		if err != nil {
			continue
		}
		v := st.Verdict
		if v == "" {
			v = gatePending
		}
		out[g] = v
	}
	return out
}

// statusAlignment builds the alignment digest from alignment.json, reusing
// the same reader facts.go uses — nil when the run has no alignment.json
// yet, never an error (spec §11).
func statusAlignment(bdir string) *alignmentDigest {
	a, err := readAlignment(bdir)
	if err != nil || a == nil {
		return nil
	}
	d := &alignmentDigest{Confirmed: a.Confirmed, Counts: map[string]int{}}
	for _, v := range a.Verdicts {
		d.Counts[v.Verdict]++
		switch v.Verdict {
		case "narrowed", "dropped", "contradicted":
			d.Contraction = append(d.Contraction, v.ID)
		case "widened":
			d.Creep = append(d.Creep, v.ID)
		}
	}
	return d
}

// statusGoals converts parsed goals.md items to the status view's shape.
func statusGoals(items []goals.Goal) []statusGoal {
	list := make([]statusGoal, 0, len(items))
	for _, it := range items {
		list = append(list, statusGoal{ID: it.ID, Text: it.Text, Signal: it.Signal})
	}
	return list
}

// readStatusSession reads the run's holder from the untracked
// logs/session.json (spec §4.6). A lock that cannot be read reads as none:
// `takt status` is read-only and must not fail on a lock it is in no
// position to judge — `takt doctor` is what names an unreadable one, and
// `takt unlock` is what clears it.
func readStatusSession(bdir string) *bundle.Session {
	sess, err := bundle.ReadSession(bdir)
	if err != nil {
		return nil
	}
	return sess
}

// statusSession renders the holder for the --json document: who holds the
// run, from where, when they last called and how long ago that was; nil —
// so the key is `null` — when nobody holds it (spec §11).
func statusSession(s *bundle.Session) map[string]any {
	if s == nil {
		return nil
	}
	return map[string]any{
		"id":        s.ID,
		"host":      s.Host,
		"heartbeat": s.Heartbeat.Format(time.RFC3339),
		"age":       time.Since(s.Heartbeat).Round(time.Second).String(),
	}
}

// statusJSON renders info as the --json document (keys fixed by spec §11).
func statusJSON(info statusInfo) map[string]any {
	goalsOut := make([]map[string]any, 0, len(info.Goals))
	for _, g := range info.Goals {
		goalsOut = append(goalsOut, map[string]any{"id": g.ID, "text": g.Text, "signal": g.Signal})
	}
	doc := map[string]any{
		keySlug: info.Slug, "phase": info.Phase, keyBranch: info.Branch, keyBranchAdopted: info.BranchAdopted,
		keyBase: info.Base, keyBaseSHA: info.BaseSHA,
		keyTasks:       map[string]any{"total": info.TasksTotal, "by_status": info.TasksByStatus, "items": info.Tasks},
		"gates":        info.Gates,
		"gates_live":   info.GatesLive,
		"active_wave":  info.ActiveWave,
		"pending_gate": info.PendingGate,
		keyGoals:       goalsOut,
		"goals_frozen": info.GoalsFrozen,
		"alignment":    info.Alignment,
		keySession:     statusSession(info.Session),
	}
	if info.Finish != nil {
		doc["finish"] = info.Finish
	}
	if info.Internal != nil {
		doc["internal_review"] = info.Internal
	}
	return doc
}

// renderStatus is the one-screen human view.
func renderStatus(info statusInfo) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s  phase=%s  branch=%s (base %s)\n", info.Slug, info.Phase, info.Branch, info.Base)
	if info.Session == nil {
		b.WriteString("session: none\n")
	} else {
		fmt.Fprintf(&b, "session: %s@%s, heartbeat %s ago\n",
			info.Session.ID, info.Session.Host, time.Since(info.Session.Heartbeat).Round(time.Second))
	}
	c := info.TasksByStatus
	fmt.Fprintf(&b, "tasks: %d total — pending %d, done %d, failed %d, blocked %d, waived %d\n",
		info.TasksTotal, c[bundle.StatusPending], c[bundle.StatusDone], c[bundle.StatusFailed],
		c[bundle.StatusBlocked], c[bundle.StatusWaived])
	for _, t := range info.Tasks {
		if t.Model == "" {
			fmt.Fprintf(&b, "  #%d wave %d %s (%s)\n", t.ID, t.Wave, t.Status, t.Class)
			continue
		}
		fmt.Fprintf(&b, "  #%d wave %d %s (%s, attempt %d, %s)\n", t.ID, t.Wave, t.Status, t.Class, t.Attempt, t.Model)
	}
	if info.ActiveWave != nil {
		fmt.Fprintf(&b, "active wave: %d (attempt %d, since %s)\n",
			info.ActiveWave.N, info.ActiveWave.Attempt, info.ActiveWave.StartedAt.Format("15:04:05"))
	}
	if info.Internal != nil {
		fmt.Fprintf(&b, "internal review: %s\n", internalLine(info.Internal))
	}
	if info.PendingGate != nil {
		fmt.Fprintf(&b, "open gate: %s\n", info.PendingGate.ID)
	}
	if info.Gates != nil {
		fmt.Fprintf(&b, "gates: spec=%s plan=%s\n", info.Gates["spec"], info.Gates["plan"])
	}
	renderLiveGates(&b, info.GatesLive)
	if len(info.Goals) > 0 {
		b.WriteString("goals:\n")
		for _, g := range info.Goals {
			fmt.Fprintf(&b, "  %s — %s (%s)\n", g.ID, g.Text, g.Signal)
		}
	}
	if info.Alignment != nil {
		fmt.Fprintf(&b, "alignment: %s\n", alignmentLine(info.Alignment))
	}
	if info.Finish != nil {
		renderFinish(&b, info.Finish)
	}
	return b.String()
}

// renderFinish prints the finish-phase block: the verify verdict, each
// goal's verdict or waiver, and the disposition — the text form of the
// JSON "finish" key (spec §11).
func renderFinish(b *strings.Builder, f *finishStatus) {
	fmt.Fprintf(b, "verify: %s\n", verifyLine(f))
	if len(f.Goals) > 0 {
		fmt.Fprintf(b, "goals: %s\n", goalsLine(f.Goals))
	}
	fmt.Fprintf(b, "disposition: %s\n", dispositionLine(f))
}

// verifyLine renders verify's three states: no record yet, a failed run, or
// a passed run naming the short SHA it passed at.
func verifyLine(f *finishStatus) string {
	switch {
	case f.VerifyPassed == nil:
		return "not yet"
	case *f.VerifyPassed:
		return "passed at " + shortSHA(f.VerifiedSHA)
	default:
		return keyFailed
	}
}

// shaShortLen is how many characters of a SHA the finish block's text lines
// show, matching `git log --oneline`'s abbreviation.
const shaShortLen = 7

func shortSHA(sha string) string {
	if len(sha) > shaShortLen {
		return sha[:shaShortLen]
	}
	return sha
}

// goalsLine renders "G1 achieved, G2 waived (docs later)": each goal id in
// sorted order (the map itself carries no order) with its verdict, or its
// waiver reason in parens when it was waived rather than achieved.
func goalsLine(verdicts map[string]string) string {
	ids := make([]string, 0, len(verdicts))
	for id := range verdicts {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	parts := make([]string, 0, len(ids))
	for _, id := range ids {
		parts = append(parts, id+" "+goalWord(verdicts[id]))
	}
	return strings.Join(parts, ", ")
}

// goalWord turns one goal's map value ("achieved", or "waived: <reason>")
// into its text-line word ("achieved", or "waived (<reason>)").
func goalWord(v string) string {
	if reason, ok := strings.CutPrefix(v, "waived: "); ok {
		return "waived (" + reason + ")"
	}
	return v
}

// dispositionLine renders "merge (applied)" / "pr (pending)" / "none".
func dispositionLine(f *finishStatus) string {
	if f.Disposition == "" {
		return "none"
	}
	state := "pending"
	if f.Applied {
		state = "applied"
	}
	return f.Disposition + " (" + state + ")"
}

// renderLiveGates prints the "gates (live): spec=… plan=…" line, spec then
// plan, skipping a gate whose artifacts are not all present yet.
func renderLiveGates(b *strings.Builder, live map[string]string) {
	if len(live) == 0 {
		return
	}
	b.WriteString("gates (live):")
	for _, g := range []string{gate.Spec, gate.Plan} {
		if v, ok := live[g]; ok {
			fmt.Fprintf(b, " %s=%s", g, v)
		}
	}
	b.WriteString("\n")
}

// alignmentVerdictOrder is the canonical order the alignment summary line
// lists verdicts in.
var alignmentVerdictOrder = []string{"covered", "narrowed", "dropped", "widened", "contradicted"}

// alignmentLine renders the alignment digest: verdict counts, then the
// clause ids that narrowed, dropped or contradicted the ask (contraction)
// and the ones that widened it (creep), each only when non-empty.
func alignmentLine(a *alignmentDigest) string {
	counts := make([]string, 0, len(a.Counts))
	for _, v := range alignmentVerdictOrder {
		if n := a.Counts[v]; n > 0 {
			counts = append(counts, fmt.Sprintf("%d %s", n, v))
		}
	}
	line := strings.Join(counts, ", ")
	if len(a.Contraction) > 0 {
		line += fmt.Sprintf(" (contraction: %s)", strings.Join(a.Contraction, ", "))
	}
	if len(a.Creep) > 0 {
		line += fmt.Sprintf(" (creep: %s)", strings.Join(a.Creep, ", "))
	}
	return line
}

// internalLine renders the internal review's one-line state: skipped, still
// waiting on lenses, waiting on the verifier, or the verified counts
// (two-layers design §5.7).
func internalLine(in *internalStatus) string {
	switch {
	case in.Skipped:
		return gateSkipped
	case in.LensesRecorded < in.LensesTotal:
		return fmt.Sprintf("%d/%d lenses recorded", in.LensesRecorded, in.LensesTotal)
	case in.VerifyPending:
		return fmt.Sprintf("verify pending (%d candidates)", in.Candidates)
	default:
		return fmt.Sprintf("%d candidates, %d confirmed", in.Candidates, in.Confirmed)
	}
}
