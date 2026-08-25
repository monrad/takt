package gitx_test

import (
	"os"
	"testing"

	"github.com/monrad/takt/internal/testutil"
)

// TestMain isolates this whole test binary from the developer's git
// configuration (review finding 5): gitx inherits [os.Environ], so a global
// core.hooksPath or core.excludesFile would otherwise leak into these tests.
func TestMain(m *testing.M) {
	os.Exit(testutil.RunHermetic(m))
}
