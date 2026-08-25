package cli

// This file exposes a few unexported helpers to the external cli_test
// package so their edge cases can be unit-tested directly instead of only
// through a whole command. It is compiled into tests only.

// CommandContext is [commandContext].
var CommandContext = commandContext

// ParseInterspersed is [parseInterspersed].
var ParseInterspersed = parseInterspersed
