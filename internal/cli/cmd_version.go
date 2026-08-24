package cli

import (
	"flag"
	"io"

	"github.com/monrad/takt/internal/version"
)

func cmdVersion(env Env) int {
	fs := flag.NewFlagSet("version", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	expect := fs.String("expect", "", "fail unless the version equals this value")
	if err := fs.Parse(env.Args); err != nil {
		return usageError(env, fs, err)
	}
	if *expect != "" && *expect != version.Version {
		return fail(env.Stderr, 1,
			"takt version "+version.Version+" does not match expected "+*expect,
			"install the takt binary matching the plugin version")
	}
	if err := writeJSON(env.Stdout, map[string]string{"version": version.Version}); err != nil {
		return 1
	}
	return 0
}
