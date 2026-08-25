package doctor

import (
	"context"
	"os"
	"path/filepath"
	"time"
)

// IndexLockStale is how old .git/index.lock must be before doctor assumes
// the git command that made it is gone (a deadline-killed pathspec commit
// leaves one behind — plan 2 backlog).
const IndexLockStale = 2 * time.Minute

const indexLockCheckName = "index-lock"

// IndexLock warns about a stranded .git/index.lock; every later git command
// in the repository fails until it is removed. It is repo-wide (spec §11):
// one lock file governs the whole repository, not one bundle, so RunWith
// runs it once per invocation instead of once per bundle.
var IndexLock = Check{Name: indexLockCheckName, RepoWide: true, Run: func(_ context.Context, in Input) []Finding {
	f := Finding{Level: levelPass, Check: indexLockCheckName, Message: "no stranded index.lock"}
	if in.RepoRoot == "" {
		return []Finding{f}
	}
	p := filepath.Join(in.RepoRoot, ".git", "index.lock")
	fi, err := os.Stat(p)
	if err != nil {
		return []Finding{f}
	}
	age := in.Now.Sub(fi.ModTime())
	if age < IndexLockStale {
		f.Message = "index.lock is " + age.Round(time.Second).String() + " old (a git command is probably running)"
		return []Finding{f}
	}
	f.Level = levelWarn
	f.Message = "stranded " + p + " (" + age.Round(time.Second).String() + " old)"
	f.Fix = "if no git command is running: rm " + p
	return []Finding{f}
}}
