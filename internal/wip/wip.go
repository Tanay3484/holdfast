// Package wip marks code that has not been written yet.
//
// The four @human packages (verify, policy, shape, replay) start out returning ErrLocked. The relay
// falls back to simpler behaviour while a function is locked, and the website shows a "WIP" tile in
// its place. Replace the stub body with a real implementation to unlock that part of the site.
package wip

import (
	"errors"
	"fmt"
)

// ErrLocked is returned by a function whose implementation has not been written yet.
var ErrLocked = errors.New("locked: not written yet")

// Locked returns ErrLocked wrapped with the name of the function that is still a stub.
func Locked(what string) error {
	return fmt.Errorf("%s: %w", what, ErrLocked)
}

// IsLocked reports whether err comes from a stub.
func IsLocked(err error) bool {
	return errors.Is(err, ErrLocked)
}
