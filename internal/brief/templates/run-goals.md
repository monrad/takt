Distil the success criteria of {{.SpecPath}} into {{.GoalsPath}} for run {{.Slug}}, in exactly this format:

# Goals — {{.Slug}}

## Anchor
```text
<the original request, verbatim — copy it exactly from state.json's topic:>
{{.Topic}}
```

## Goals
- G1 — <one testable sentence> · signal: test | command | artifact | docs · evidence: <what will prove it>
- G2 — …

Then show the list to the user with AskUserQuestion and, once they confirm it, run: takt done --step goals --slug {{.Slug}}
