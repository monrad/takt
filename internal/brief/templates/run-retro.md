Write the retrospective for run {{.Slug}} to the bundle's retro.md, next to {{.SpecPath}}.

Cover what the run set out to do ({{.Topic}}), what actually happened, and what should change next time: retries and their causes, failures and waivers with the reasons given, and anything the plan got wrong.

Then run: takt done --step retro --slug {{.Slug}}
