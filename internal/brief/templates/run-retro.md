Write the retrospective for run {{.Slug}} to {{.RetroPath}}. The facts are in {{.InputsPath}} (JSON): task and wave counts, per-wave dispatch→commit timings, retries, failures and waivers with reasons, the review findings count, the verification record and the goal verdicts.

Structure:

# Retro — {{.Slug}}

## What was built
Two or three sentences from the topic and the goal verdicts.

## What went well / what did not
Bullet points grounded in the inputs (timings, retries, failures, review findings). Name the tasks by id.

## Follow-ups
Bullet points: waived goals or tasks, overridden verification, anything the inputs show was left undone.

Then run: takt done --step retro --slug {{.Slug}}
