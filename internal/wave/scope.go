package wave

import (
	"context"
	"os"
	"path/filepath"
	"sort"

	"github.com/monrad/takt/internal/bundle"
	"github.com/monrad/takt/internal/gitx"
)

// Scope is the D6 verdict for one wave.
type Scope struct {
	PerTask    map[int][]string // task id → files it changed (⊆ declared)
	OutOfScope []Touched        // touched paths no task declared
}

// VerifyScope partitions touched paths by the tasks' declared files.
func VerifyScope(touched []Touched, tasks map[int][]string) Scope {
	owner := map[string]int{}
	for id, files := range tasks {
		for _, f := range files {
			owner[f] = id
		}
	}
	sc := Scope{PerTask: map[int][]string{}}
	for id := range tasks {
		sc.PerTask[id] = []string{}
	}
	for _, tp := range touched {
		if id, ok := owner[tp.Path]; ok {
			sc.PerTask[id] = append(sc.PerTask[id], tp.Path)
			continue
		}
		sc.OutOfScope = append(sc.OutOfScope, tp)
	}
	for id := range sc.PerTask {
		sort.Strings(sc.PerTask[id])
	}
	return sc
}

// Revert discards out-of-scope changes: tracked paths are restored from
// HEAD, untracked ones are deleted. An out-of-scope path that is an
// untracked deletion (never tracked, already gone) has nothing to restore —
// it stays reported via the caller's Scope.OutOfScope but is not counted in
// the returned reverted slice. Returns the paths actually reverted.
func Revert(ctx context.Context, repo *gitx.Repo, out []Touched) ([]string, error) {
	var reverted []string
	for _, tp := range out {
		tracked, err := repo.InHead(ctx, tp.Path)
		if err != nil {
			return reverted, err
		}
		switch {
		case tracked:
			if rerr := repo.RestorePaths(ctx, tp.Path); rerr != nil {
				return reverted, rerr
			}
		case tp.Deleted:
			continue // untracked and already gone: nothing to restore
		default:
			rmerr := os.Remove(filepath.Join(repo.Root, filepath.FromSlash(tp.Path)))
			if rmerr != nil && !os.IsNotExist(rmerr) {
				return reverted, rmerr
			}
		}
		reverted = append(reverted, tp.Path)
	}
	return reverted, nil
}

// recoveryAction is what ResetForRecovery must do to one declared file,
// decided by [classifyRecoveryFile].
type recoveryAction int

const (
	recoverySkip recoveryAction = iota
	recoveryRestore
	recoveryRemove
)

// classifyRecoveryFile decides the action for f, in the same order the
// original flat check did: unchanged-since-baseline first (user dirt
// survives untouched), then never-existed-and-still-absent, then — for a
// file tracked in HEAD that was clean at baseline — a check against HEAD so
// an untouched clean file is also left alone; anything else tracked is
// restored from HEAD, anything else untracked is removed.
func classifyRecoveryFile(
	ctx context.Context,
	repo *gitx.Repo,
	f string,
	base map[string]string,
) (recoveryAction, error) {
	h, err := hashFile(repo.Root, f)
	if err != nil {
		return recoverySkip, err
	}
	prev, dirtyAtBaseline := base[f]
	if dirtyAtBaseline && prev == h {
		return recoverySkip, nil
	}
	tracked, err := repo.InHead(ctx, f)
	if err != nil {
		return recoverySkip, err
	}
	if !dirtyAtBaseline && h == "" {
		return recoverySkip, nil // never existed, still absent
	}
	if !tracked {
		return recoveryRemove, nil
	}
	if !dirtyAtBaseline {
		// Clean at baseline: only touched if it differs from HEAD now.
		if unchangedFromHead(ctx, repo, f) {
			return recoverySkip, nil
		}
	}
	return recoveryRestore, nil
}

// ResetForRecovery resets a crashed task's declared files (spec §5.4): a
// file whose content still equals the baseline is left alone (user dirt
// survives); a changed tracked file is restored from HEAD (any user dirt
// in it is lost — a documented limitation); a changed untracked file is
// removed. Returns the files reset.
func ResetForRecovery(
	ctx context.Context,
	repo *gitx.Repo,
	files []string,
	baseline []bundle.BaselineEntry,
) ([]string, error) {
	base := map[string]string{}
	for _, e := range baseline {
		base[e.Path] = e.Hash
	}
	var reset []string
	for _, f := range files {
		action, err := classifyRecoveryFile(ctx, repo, f, base)
		if err != nil {
			return reset, err
		}
		switch action {
		case recoverySkip:
			continue
		case recoveryRestore:
			if rerr := repo.RestorePaths(ctx, f); rerr != nil {
				return reset, rerr
			}
		case recoveryRemove:
			if rmerr := os.Remove(
				filepath.Join(repo.Root, filepath.FromSlash(f)),
			); rmerr != nil &&
				!os.IsNotExist(rmerr) {
				return reset, rmerr
			}
		}
		reset = append(reset, f)
	}
	return reset, nil
}

// unchangedFromHead reports whether path currently matches HEAD. A real git
// failure (not just "there is a diff") is treated the same as "changed" —
// callers only need the bool, so no error is threaded through.
func unchangedFromHead(ctx context.Context, repo *gitx.Repo, path string) bool {
	_, err := repo.Run(ctx, "diff", "--quiet", "HEAD", "--", path)
	return err == nil
}
