Invoke the superpowers:brainstorming skill for run {{.Slug}} on this topic:

{{.Topic}}

Write the approved design to {{.SpecPath}}. It must include an "## Assumptions & Open Decisions" table with columns question | decision | rationale | source (source is `assumed` or `user-confirmed`). When the spec is written and the user has approved it, run: takt done --step brainstorm --slug {{.Slug}}
