Push branch {{.Branch}} and open a pull request against {{.Base}} for run {{.Slug}}:

    git push -u origin {{.Branch}}
    gh pr create --base {{.Base}} --fill

Ask the user before pushing if this repository has no remote yet. When the PR exists, run: takt done --step push_pr --url <pr-url> --slug {{.Slug}}
