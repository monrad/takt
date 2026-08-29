Write the retrospective for run {{.Slug}} to {{.RetroPath}}. The facts are in {{.InputsPath}} (JSON): task and wave counts, per-wave dispatch→commit timings, retries, failures and waivers with reasons, the review findings count — gate passes plus every attempt's task reviews, split as `gate_review_findings` / `task_review_findings`, review findings carried forward as follow-ups, the verification record and the goal verdicts.

Structure:

# Retro — {{.Slug}}

## What was built
Two or three sentences from the topic and the goal verdicts.

## What went well / what did not
Bullet points grounded in the inputs (timings, retries, failures, review findings). Name the tasks by id. If the inputs carry `internal_review`, ground bullets in it too: candidates vs confirmed per lens (a lens with no confirmed finding across the run is a candidate for removal from `review.lenses`), the overlap count (confirmed internal findings the cross-vendor reviewer also raised — a heuristic: same file within a few lines), and the scoped passes with whether they changed a verdict.

## Follow-ups
Bullet points: waived goals or tasks, overridden verification, anything the inputs show was left undone. Then every entry in `follow_ups` — review findings that closed with their gate instead of being acted on — as `severity — title (gate or wave/task, source: approve|override|internal)` followed by its detail — `approve` means the pass closed carrying it, `override` means the user declined it. `internal` means a lens finding the cross-vendor reviewer did not act on. Do not drop the minors: they are here precisely because nothing else will carry them.

Then run: takt done --step retro --slug {{.Slug}}
