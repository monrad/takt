// Package op defines the typed operations `takt next` returns (spec §5.2).
// The session executes exactly one Op per call. Paths inside an Op are
// absolute: the session may run from any cwd.
package op

// Kind is the op discriminator.
type Kind string

// The five op kinds (spec §5.2).
const (
	Dispatch Kind = "dispatch" // spawn subagents, record each, call next
	Ask      Kind = "ask"      // ask the user, then `takt answer`, then next
	Run      Kind = "run"      // LLM-side step, then `takt done`, then next
	Exec     Kind = "exec"     // run a takt command in the background, then next
	Stop     Kind = "stop"     // end the turn
)

// Agent is one subagent to spawn.
type Agent struct {
	Task  int    `json:"task,omitempty"`
	Agent string `json:"agent"`
	Class string `json:"class,omitempty"`
	Model string `json:"model"`
	Brief string `json:"brief"`
	Cwd   string `json:"cwd"`
	Label string `json:"label"`
	Mode  string `json:"mode,omitempty"` // alignment-auditor: clauses | verdicts
}

// Option is one answer to an ask op; the first is the recommended one.
type Option struct {
	Choice      string `json:"choice"`
	Label       string `json:"label"`
	Description string `json:"description"`
}

// Op is the single object `takt next` prints.
type Op struct {
	Op        Kind   `json:"op"`
	Narration string `json:"narration"`

	// dispatch
	Wave    *int    `json:"wave,omitempty"`
	Attempt int     `json:"attempt,omitempty"`
	Agents  []Agent `json:"agents,omitempty"`
	Record  string  `json:"record,omitempty"`

	// ask
	Gate     string         `json:"gate,omitempty"`
	Question string         `json:"question,omitempty"`
	Options  []Option       `json:"options,omitempty"`
	Context  map[string]any `json:"context,omitempty"`
	Answer   string         `json:"answer,omitempty"`

	// run
	Step         string         `json:"step,omitempty"`
	Instructions string         `json:"instructions,omitempty"`
	Inputs       map[string]any `json:"inputs,omitempty"`
	Done         string         `json:"done,omitempty"`

	// exec
	Command  string `json:"command,omitempty"`
	TimeoutS int    `json:"timeout_s,omitempty"`

	// stop
	Reason string `json:"reason,omitempty"`
}
