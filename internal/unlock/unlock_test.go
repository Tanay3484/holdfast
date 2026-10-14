package unlock

import (
	"errors"
	"testing"

	"github.com/Tanay3484/holdfast/internal/wip"
)

func level(checks ...func() error) Level {
	l := Level{Key: "k"}
	for i, c := range checks {
		l.Checks = append(l.Checks, Check{Name: string(rune('a' + i)), Run: c})
	}
	return l
}

var (
	pass   = func() error { return nil }
	fail   = func() error { return errors.New("wrong") }
	locked = func() error { return wip.Locked("x") }
	boom   = func() error { panic("oops") }
)

func TestEvaluateStates(t *testing.T) {
	cases := []struct {
		name   string
		l      Level
		state  State
		passed int
	}{
		{"all stubs", level(locked, locked), Locked, 0},
		{"all pass", level(pass, pass, pass), Unlocked, 3},
		{"some pass", level(pass, fail, locked), Cracked, 1},
		{"runs but wrong", level(fail, fail), Cracked, 0},
		{"a panic counts as a failure", level(pass, boom), Cracked, 1},
		{"no checks is never unlocked", level(), Locked, 0},
	}
	for _, c := range cases {
		r := Evaluate(c.l)
		if r.State != c.state || r.Passed != c.passed {
			t.Errorf("%s: got %s %d/%d, want %s %d", c.name, r.State, r.Passed, r.Total, c.state, c.passed)
		}
		if len(r.Outcomes) != len(c.l.Checks) {
			t.Errorf("%s: %d outcomes for %d checks", c.name, len(r.Outcomes), len(c.l.Checks))
		}
	}
}

func TestBoard(t *testing.T) {
	a, b := level(pass), level(locked)
	a.Key, b.Key = "a", "b"
	board := EvaluateAll([]Level{a, b})
	if board.Unlocked != 1 || board.Done() || !board.Is("a") || board.Is("b") || board.Is("missing") {
		t.Fatalf("board: %+v", board)
	}
}

func TestRealLevelsAreWellFormed(t *testing.T) {
	seen := map[string]bool{}
	for i, l := range Levels() {
		if l.N != i+1 || l.Key == "" || l.File == "" || l.Task == "" || len(l.Checks) == 0 || seen[l.Key] {
			t.Errorf("level %d is malformed: %+v", i+1, l)
		}
		seen[l.Key] = true
		// Running them must never panic, whatever state the @human code is in.
		Evaluate(l)
	}
}
