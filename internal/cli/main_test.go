package cli_test

import (
	"os"
	"testing"

	"github.com/monrad/takt/internal/testutil"
)

// TestMain isolates this whole test binary from the developer's git
// configuration (review finding 5): the commands under test drive git
// through gitx, which inherits [os.Environ].
func TestMain(m *testing.M) {
	os.Exit(testutil.RunHermetic(m))
}
