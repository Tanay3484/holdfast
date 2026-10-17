package diagnose

import (
	"fmt"
	"strings"

	"github.com/Tanay3484/holdfast/internal/shape"
)

// Rename is a field that moved: a value the last accepted event had at From is now at To.
type Rename struct {
	From, To string // display paths, without the leading "$."
}

// Renames pairs a removed value with an added value whose path reads the same once punctuation and
// case are ignored, so customer_id → customer.id or customerId → customer_id count as one rename.
// Code finds these so the model never has to work out which side moved.
func Renames(changes []shape.Change) []Rename {
	var out []Rename
	used := map[int]bool{}
	for _, rm := range changes {
		if rm.Kind != shape.Removed || container(rm.Was) {
			continue
		}
		for j, add := range changes {
			if used[j] || add.Kind != shape.Added || container(add.Now) || squash(add.Path) != squash(rm.Path) {
				continue
			}
			used[j] = true
			out = append(out, Rename{From: display(rm.Path), To: display(add.Path)})
			break
		}
	}
	return out
}

// Explain turns changes into sentences, renames first. Containers that only appear because of a
// rename (the new "customer" object around customer.id) are left out.
func Explain(changes []shape.Change) []string {
	renames := Renames(changes)
	moved := map[string]bool{}
	var lines []string
	for _, r := range renames {
		moved[r.From], moved[r.To] = true, true
		lines = append(lines, fmt.Sprintf("Renamed: the sender now sends %s where it used to send %s.", r.To, r.From))
	}
	for _, c := range changes {
		p := display(c.Path)
		if moved[p] {
			continue
		}
		switch c.Kind {
		case shape.Removed:
			lines = append(lines, fmt.Sprintf("No longer sent: %s (it was a %s).", p, c.Was))
		case shape.Retyped:
			lines = append(lines, fmt.Sprintf("Different type: %s was a %s and is now a %s.", p, c.Was, c.Now))
		case shape.Added:
			if !container(c.Now) {
				lines = append(lines, fmt.Sprintf("New: %s (a %s).", p, c.Now))
			}
		}
	}
	return lines
}

func container(k shape.Kind) bool { return k == shape.Object || k == shape.Array }

func display(path string) string { return strings.TrimPrefix(strings.TrimPrefix(path, "$"), ".") }

func squash(path string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(path) {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' {
			b.WriteRune(r)
		}
	}
	return b.String()
}
