package finish

import (
	"fmt"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/monrad/takt/internal/bundle"
	"github.com/monrad/takt/internal/goals"
)

// PR is the pull request a finished run describes: the title `gh pr create`
// takes after --title and the body it takes through --body-file (#36).
// Both are derived from what the run itself wrote — the spec's heading and
// opening paragraph, the goals with their verdicts, a pointer at the bundle
// — rather than from `--fill`, which for a takt run titles the pull request
// after a branch name and fills its body with bookkeeping commits.
type PR struct{ Title, Body string }

// prTitleMaxRunes caps the fallback title at the topic's first 72 runes: the
// width a commit subject is conventionally kept under. It counts runes, not
// bytes, so a topic written in a multi-byte script is cut where a reader
// would cut it rather than in the middle of a character.
const prTitleMaxRunes = 72

// notAssessed is what a goal that no verdict covers is rendered with: the
// body says the goal was not judged rather than leaving it out, because a
// missing line is indistinguishable from a goal nobody wrote down.
const notAssessed = "not assessed"

// issuesSentence introduces the `## Issues` section when the run's goals are
// on. The two sections answer different questions — which issues the run was
// started to fix, and which of them it proved — and the sentence says so, so
// that a reader does not take the closing line for a claim of success.
const issuesSentence = "These are the issues this run set out to fix; " +
	"`## Goals` above says which of them it proved."

// issuesSentenceNoGoals is the same sentence for a --no-goals run: the clause
// that points at `## Goals` is dropped, because the body renders no such
// section for it to point at.
const issuesSentenceNoGoals = "These are the issues this run set out to fix."

// issueRef matches the three token forms a topic names an issue with, as one
// alternation in the order they are tried. Cross-repository comes first so
// that `monrad/takt#71` is taken whole and its tail is never read as a bare
// `#71`; the issue URL's `https?://` prefix keeps a bare `/issues/12`
// fragment, which is not a link, out of the closing line; the bare number is
// last, and its boundaries are checked by hand in bareRefIsBounded because
// RE2 has no lookbehind.
var issueRef = regexp.MustCompile(`[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+#\d+|https?://\S+?/issues/\d+|#\d+`)

// BuildPR derives the pull request from the run's artifacts. spec is
// spec.md's text, topic the run's topic, gs the goals in goals.md order —
// nil when the run's goals are off, and then the whole `## Goals` section is
// omitted — rec the finish/goals.json record (nil when the run wrote none,
// so every goal is "not assessed"), and bundleRel the bundle directory as
// the pull request should name it. It is pure: nothing here reads the tree.
func BuildPR(spec, topic string, gs []goals.Goal, rec *GoalsRecord, bundleRel string) PR {
	lines := strings.Split(strings.ReplaceAll(spec, "\r\n", "\n"), "\n")
	h1 := -1
	for i, ln := range lines {
		if strings.HasPrefix(ln, "# ") {
			h1 = i
			break
		}
	}
	var sections []string
	if para := firstParagraph(lines[h1+1:]); para != "" {
		sections = append(sections, para)
	}
	if gs != nil {
		sections = append(sections, goalsSection(gs, rec))
	}
	if issues := issuesSection(topic, gs != nil); issues != "" {
		sections = append(sections, issues)
	}
	sections = append(sections,
		"## Run\n\nBundle: "+bundleRel+"/ — spec.md, plan.md, reviews/, retro.md")
	return PR{Title: prTitle(lines, h1, topic), Body: strings.Join(sections, "\n\n") + "\n"}
}

// PRPath is where `next` writes the body the push_pr op points at.
func PRPath(bundleDir string) string { return filepath.Join(bundleDir, "finish", "pr.md") }

// WritePR writes the body atomically, so a `next` that dies mid-write leaves
// the previous body rather than half of the new one.
func WritePR(bundleDir, body string) error {
	return bundle.WriteFileAtomic(PRPath(bundleDir), []byte(body))
}

// prTitle is the text of the spec's H1 — the name the spec gives the change
// — or the topic cut to prTitleMaxRunes when the spec has no heading. h1 is
// the index of that heading in lines, or -1.
func prTitle(lines []string, h1 int, topic string) string {
	if h1 >= 0 {
		return strings.TrimSpace(strings.TrimPrefix(lines[h1], "# "))
	}
	r := []rune(topic)
	if len(r) > prTitleMaxRunes {
		r = r[:prTitleMaxRunes]
	}
	return strings.TrimSpace(string(r))
}

// firstParagraph is the spec's opening prose: the first run of non-blank
// lines in lines, with blank lines and further headings skipped on the way
// — a spec whose H1 is followed by `## Why` opens at the prose under that
// heading, not at the heading itself. Empty when there is no prose at all.
func firstParagraph(lines []string) string {
	var out []string
	for _, ln := range lines {
		blank := strings.TrimSpace(ln) == ""
		if len(out) == 0 {
			if blank || strings.HasPrefix(ln, "#") {
				continue
			}
		} else if blank {
			break
		}
		out = append(out, ln)
	}
	return strings.Join(out, "\n")
}

// goalsSection lists every goal of the run with what the run decided about
// it, in goals.md order — the same order the record and the retro use.
func goalsSection(gs []goals.Goal, rec *GoalsRecord) string {
	out := []string{"## Goals", ""}
	for _, g := range gs {
		out = append(out, fmt.Sprintf("- %s — %s — %s", g.ID, g.Text, goalOutcome(g.ID, rec)))
	}
	return strings.Join(out, "\n")
}

// goalOutcome is the word the body puts against one goal. A waiver wins over
// a verdict: it is the user's own decision about a goal the assessor did not
// find achieved, and it is the decision a reviewer of the pull request has
// to see. Presence in the map is the waiver, not the reason it carries — a
// record that records a waiver with no reason still records a waiver, and
// rendering it as its unmet verdict would hide a decision the user made.
func goalOutcome(id string, rec *GoalsRecord) string {
	if rec == nil {
		return notAssessed
	}
	if reason, ok := rec.Waived[id]; ok {
		return "waived (" + reason + ")"
	}
	for _, v := range rec.Verdicts {
		if v.ID == id {
			return v.Verdict
		}
	}
	return notAssessed
}

// issuesSection is the `## Issues` section: the sentence, a blank line and a
// closing line naming every issue reference the topic carries. The keyword is
// repeated per reference — `Closes #66, closes #71` — because GitHub links
// only the first issue of a bare comma list, so one keyword for four issues
// closes one. goalsOn selects the sentence. Empty when the topic names no
// issue: a heading over nothing reads as an omission.
func issuesSection(topic string, goalsOn bool) string {
	refs := issueRefs(topic)
	if len(refs) == 0 {
		return ""
	}
	sentence := issuesSentenceNoGoals
	if goalsOn {
		sentence = issuesSentence
	}
	closing := make([]string, len(refs))
	for i, ref := range refs {
		keyword := "closes "
		if i == 0 {
			keyword = "Closes "
		}
		closing[i] = keyword + ref
	}
	return "## Issues\n\n" + sentence + "\n\n" + strings.Join(closing, ", ")
}

// issueRefs is every issue reference the topic names, verbatim and in the
// topic's own order, de-duplicated by rendered token. Two different forms
// naming one issue both survive: BuildPR is pure and knows neither the
// repository nor the host, so it cannot prove `#71` and an issue URL are the
// same issue — GitHub resolves them and closes it once.
func issueRefs(topic string) []string {
	var out []string
	seen := make(map[string]bool)
	for _, m := range issueRef.FindAllStringIndex(topic, -1) {
		ref := topic[m[0]:m[1]]
		if strings.HasPrefix(ref, "#") && !bareRefIsBounded(topic, m[0], m[1]) {
			continue
		}
		if seen[ref] {
			continue
		}
		seen[ref] = true
		out = append(out, ref)
	}
	return out
}

// bareRefIsBounded is the lookaround RE2 will not do for the bare `#N` form:
// the match must not be preceded by `/` or by a word character — which is
// what rejects `/#71`, `owner/#71` and `takt#71`, the last being the tail of
// a cross-repository token the alternation declined — and must not be
// followed by one, which rejects `#71b`.
func bareRefIsBounded(topic string, start, end int) bool {
	if start > 0 {
		if b := topic[start-1]; b == '/' || isWordByte(b) {
			return false
		}
	}
	return end >= len(topic) || !isWordByte(topic[end])
}

// isWordByte reports whether b is one of the ASCII bytes that make a word:
// the boundary class the bare form is checked against. A byte of a multi-byte
// rune is not one, so a topic written in another script still names its issue.
func isWordByte(b byte) bool {
	return b == '_' ||
		('0' <= b && b <= '9') ||
		('a' <= b && b <= 'z') ||
		('A' <= b && b <= 'Z')
}
