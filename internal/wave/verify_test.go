package wave_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/monrad/takt/internal/wave"
)

func TestRunVerify(t *testing.T) {
	t.Parallel()
	res := wave.RunVerify(
		context.Background(),
		t.TempDir(),
		[]string{"true", "echo hi && false", "sleep 3"},
		500*time.Millisecond,
	)
	if len(res) != 3 {
		t.Fatal(res)
	}
	if !res[0].Passed || res[0].Exit != 0 {
		t.Fatalf("true: %+v", res[0])
	}
	if res[1].Passed || res[1].Exit != 1 || !strings.Contains(res[1].Tail, "hi") {
		t.Fatalf("false: %+v", res[1])
	}
	if res[2].Passed || !res[2].TimedOut {
		t.Fatalf("timeout: %+v", res[2])
	}
}

func TestRunVerifyTailIsBounded(t *testing.T) {
	t.Parallel()
	res := wave.RunVerify(context.Background(), t.TempDir(), []string{"seq 1 1000"}, 5*time.Second)
	if n := strings.Count(res[0].Tail, "\n"); n > wave.TailLines {
		t.Fatalf("tail has %d lines", n)
	}
	if !strings.Contains(res[0].Tail, "1000") {
		t.Fatal("tail keeps the end")
	}
}
