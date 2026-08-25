package cli

import "github.com/monrad/takt/internal/goals"

// This file exposes a few unexported helpers to the external cli_test
// package so their edge cases can be unit-tested directly instead of only
// through a whole command. It is compiled into tests only.

// CommandContext is [commandContext].
var CommandContext = commandContext

// ParseInterspersed is [parseInterspersed].
var ParseInterspersed = parseInterspersed

// GoalsHash is [goals.Hash], the hash validateOpts binds a plan's spec_hash
// to, exposed so a test can build a plan index the validator accepts.
var GoalsHash = goals.Hash
