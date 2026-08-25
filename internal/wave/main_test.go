package wave_test

import (
	"os"
	"testing"

	"github.com/monrad/takt/internal/testutil"
)

func TestMain(m *testing.M) { os.Exit(testutil.RunHermetic(m)) }
