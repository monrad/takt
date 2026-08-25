package cli_test

import (
	"testing"

	"github.com/monrad/takt/internal/testutil"
)

func TestAnswerOnNoPendingGateIsIgnored(t *testing.T) {
	t.Parallel()
	root, _ := setupRun(t)
	code, o, _ := runIn(t, root, nil, "answer", "--gate", "gate_review", "--choice", "revise", "--slug", "demo")
	if code != 0 || o["ignored"] != true {
		t.Fatalf("%d %v", code, o)
	}
	testutil.Git(t, root, "status", "--porcelain")
}
