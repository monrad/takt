package cli

import (
	"context"
	"errors"
	"maps"
	"os"
	"strings"

	"github.com/monrad/takt/internal/bundle"
	"github.com/monrad/takt/internal/finish"
)

// markVerified writes the record, sets verified_sha and records the event.
// Every path that declares HEAD verified goes through here.
func markVerified(_ context.Context, tgt *runTarget, rec finish.VerifyRecord, data map[string]any) error {
	rec.Passed = true
	if err := finish.WriteVerify(tgt.bdir, rec); err != nil {
		return err
	}
	sha := rec.SHA
	tgt.st.VerifiedSHA = &sha
	if err := bundle.SaveState(tgt.bdir, tgt.st); err != nil {
		return err
	}
	ev := map[string]any{keySHA: sha, keyPassed: true}
	maps.Copy(ev, data)
	return bundle.AppendEvent(tgt.bdir, "verify", ev)
}

// answerVerification applies verification_failed: fix drops the record so
// the next call re-verifies; override verifies HEAD with a recorded reason;
// abort only ends the turn (the gate returns next call, like wave_failures/stop).
func answerVerification(ctx context.Context, tgt *runTarget, choice, reason string) (bool, error) {
	switch choice {
	case "fix":
		return false, dropVerify(tgt.bdir)
	case "override":
		if strings.TrimSpace(reason) == "" {
			return false, errors.New("override needs --reason")
		}
		rec, err := finish.ReadVerify(tgt.bdir)
		if err != nil {
			return false, err
		}
		if rec == nil {
			return false, errors.New("no verification record to override")
		}
		rec.Overridden = reason
		return false, markVerified(ctx, tgt, *rec, map[string]any{"overridden": reason})
	case "abort":
		return true, nil
	}
	return false, errorf("unknown choice %q for verification_failed", choice)
}

// answerNoVerification applies no_verification: specify stores a command
// and re-arms verify; proceed verifies HEAD with nothing run.
func answerNoVerification(ctx context.Context, tgt *runTarget, choice, reason string) (bool, error) {
	switch choice {
	case "specify":
		if err := finish.AppendExtra(tgt.bdir, reason); err != nil {
			return false, err
		}
		return false, dropVerify(tgt.bdir)
	case "proceed":
		head, err := tgt.ws.Repo.HeadSHA(ctx)
		if err != nil {
			return false, err
		}
		rec := finish.VerifyRecord{SHA: head, NoCommands: true, Skipped: true, At: timeNow()}
		return false, markVerified(ctx, tgt, rec, map[string]any{"skipped": true, keyNoCommands: true})
	}
	return false, errorf("unknown choice %q for no_verification", choice)
}

// dropVerify removes the record; absence is not an error.
func dropVerify(bdir string) error {
	err := os.Remove(finish.VerifyPath(bdir))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}
