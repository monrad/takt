Push branch {{.Branch}} and open a pull request against {{.Base}} for run {{.Slug}}:

    git push -u origin {{.Branch}}
    gh pr create --base {{.Base}} --title '{{.PRTitleQuoted}}' --body-file {{.PRBodyPath}}

The title and the body file were generated from this run — the spec's opening paragraph, the goals with their verdicts, and a pointer to the bundle — so read the body and edit it before pushing if it needs more.

Ask the user before pushing if this repository has no remote yet. When the PR exists, run: takt done --step push_pr --url <pr-url> --slug {{.Slug}}
