Write the retrospective for run {{.Slug}}.

Start from the skeleton: copy {{.SkeletonPath}} to {{.RetroPath}} and fill each
`<!-- prose: … -->` slot. Do not rewrite the rendered sections; they are the record.

The retro has seven sections:

## What shipped
Rendered as a table of commits. The prose slot takes two or three sentences on what shipped.

## Decisions
Rendered from gate answers with their reasons, waivers, the spec's user-confirmed
assumptions and the disposition. No prose slot here.

## What went well / what was hard
The prose slot is the session's own account of driving this run — the reviewer that
contradicted itself, the waiver whose grounds the tree disproves, the tool that misbehaved.

## Not proven
Seeded with waived goals and tasks and any overridden or skipped verification. The prose
slot takes whatever else a reader must not assume is true.

## Lessons
The prose slot is the session's own account of driving this run, written for the next run
in this repository.

## Follow-ups
Rendered from follow-ups.json: blocking and major entries in full, minors and nits as a
count and a pointer to the file.

## Numbers
Rendered verbatim from {{.InputsPath}}. This is the only section "grounded in the inputs" applies to — cite {{.InputsPath}} where it backs a claim. The session's own observations belong in What went well / what was hard and Lessons, and are invited there.

A rewrite from a fresh session has no observations: write what the skeleton and inputs
support and do not invent an account of a run you did not drive.

Then run: takt done --step retro --slug {{.Slug}}
