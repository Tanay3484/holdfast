// Package unlock is the progress game. Each @human package is a level; a level is unlocked when every
// one of its known-answer checks passes. The website and `go run ./cmd/unlock` both read from here.
//
// The checks are a quick sanity gate, not a test suite. Write real tests next to your code too.
package unlock

import (
	"fmt"
	"sync"
	"time"

	"github.com/Tanay3484/holdfast/internal/wip"
)

// State of one level.
type State string

const (
	Locked   State = "locked"   // the stub is still in place
	Cracked  State = "cracked"  // some code runs, but not every check passes yet
	Unlocked State = "unlocked" // every check passes
)

// Check is one known-answer probe. Run returns nil on pass, an error wrapping wip.ErrLocked while the
// stub is in place, or an error saying what was expected.
type Check struct {
	Name string
	Run  func() error
}

// Level is one @human task.
type Level struct {
	N       int
	Key     string // stable id used by the web UI: "signature", "retries", "changes", "resend"
	Title   string
	File    string // the file to open
	Task    string // id in tasks.md
	Reveals string // what unlocking it shows on the site
	Checks  []Check
}

// Failure is a check that did not pass.
type Failure struct {
	Check  string
	Detail string
}

// Outcome is one check after it ran.
type Outcome struct {
	Name   string
	Passed bool
	Detail string
}

// Result is a level after its checks have run.
type Result struct {
	Level
	State    State
	Passed   int
	Total    int
	Failing  []Failure
	Outcomes []Outcome
}

// Percent is how far through the level's checks we are.
func (r Result) Percent() int {
	if r.Total == 0 {
		return 0
	}
	return r.Passed * 100 / r.Total
}

// Evaluate runs a level's checks. A check that panics counts as failing, never crashes the caller.
func Evaluate(l Level) Result {
	r := Result{Level: l, Total: len(l.Checks)}
	locked := 0
	for _, c := range l.Checks {
		err := run(c)
		switch {
		case err == nil:
			r.Passed++
			r.Outcomes = append(r.Outcomes, Outcome{c.Name, true, ""})
			continue
		case wip.IsLocked(err):
			locked++
			r.Failing = append(r.Failing, Failure{c.Name, "not written yet"})
		default:
			r.Failing = append(r.Failing, Failure{c.Name, err.Error()})
		}
		r.Outcomes = append(r.Outcomes, Outcome{c.Name, false, r.Failing[len(r.Failing)-1].Detail})
	}
	switch {
	case r.Total > 0 && r.Passed == r.Total:
		r.State = Unlocked
	case locked == r.Total:
		r.State = Locked
	default:
		r.State = Cracked
	}
	return r
}

func run(c Check) (err error) {
	defer func() {
		if p := recover(); p != nil {
			err = fmt.Errorf("panicked: %v", p)
		}
	}()
	return c.Run()
}

// Board is every level's result plus the overall state.
type Board struct {
	Levels   []Result
	Unlocked int
	Total    int
}

// Done reports whether every level is unlocked.
func (b Board) Done() bool { return b.Total > 0 && b.Unlocked == b.Total }

// Get returns the result for a level key. Unknown keys come back locked.
func (b Board) Get(key string) Result {
	for _, r := range b.Levels {
		if r.Key == key {
			return r
		}
	}
	return Result{State: Locked}
}

// Is reports whether the level with this key is unlocked.
func (b Board) Is(key string) bool { return b.Get(key).State == Unlocked }

// EvaluateAll runs every level.
func EvaluateAll(levels []Level) Board {
	b := Board{Total: len(levels)}
	for _, l := range levels {
		r := Evaluate(l)
		if r.State == Unlocked {
			b.Unlocked++
		}
		b.Levels = append(b.Levels, r)
	}
	return b
}

// Current returns the board for the real levels. Code does not change while the server runs, so the
// result is computed once and cached.
var Current = sync.OnceValue(func() Board { return EvaluateAll(Levels()) })

// now is fixed so the checks are repeatable.
var now = time.Date(2026, 10, 14, 12, 0, 0, 0, time.UTC)
