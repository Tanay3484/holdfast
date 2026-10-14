// Command unlock shows how far through the four @human levels you are.
//
//	go run ./cmd/unlock          the board
//	go run ./cmd/unlock -v       every check, passing or not
package main

import (
	"flag"
	"fmt"
	"strings"

	"github.com/Tanay3484/holdfast/internal/unlock"
)

const (
	reset  = "\033[0m"
	bold   = "\033[1m"
	dim    = "\033[2m"
	green  = "\033[32m"
	amber  = "\033[33m"
	grey   = "\033[90m"
	bright = "\033[97m"
)

func main() {
	verbose := flag.Bool("v", false, "list every check")
	flag.Parse()

	b := unlock.Current()
	fmt.Println()
	fmt.Printf("  %sHOLDFAST · UNLOCK BOARD%s%s  %d of %d levels%s\n\n", bold, reset, grey, b.Unlocked, b.Total, reset)

	nextShown := false
	for _, r := range b.Levels {
		colour, label := grey, "LOCKED"
		switch r.State {
		case unlock.Unlocked:
			colour, label = green, "UNLOCKED"
		case unlock.Cracked:
			colour, label = amber, fmt.Sprintf("%d/%d checks", r.Passed, r.Total)
		}
		fmt.Printf("  %s%s%s  Level %d  %-16s %s%s%s\n", colour, bar(r.Percent()), reset, r.N, r.Title, colour, label, reset)

		if r.State != unlock.Unlocked && !nextShown {
			nextShown = true
			fmt.Printf("  %s           next → open %s%s%s (%s)%s\n", dim, bright, r.File, reset+dim, r.Task, reset)
			fmt.Printf("  %s           reveals: %s%s\n", dim, r.Reveals, reset)
			if r.State == unlock.Cracked {
				f := r.Failing[0]
				fmt.Printf("  %s           first failing check: %s%s\n", amber, f.Check, reset)
				fmt.Printf("  %s           %s%s\n", dim, indent(f.Detail), reset)
			}
		}
		if *verbose {
			failing := map[string]string{}
			for _, f := range r.Failing {
				failing[f.Check] = f.Detail
			}
			for _, c := range r.Checks {
				if d, bad := failing[c.Name]; bad {
					fmt.Printf("               %s✗ %s%s %s(%s)%s\n", amber, c.Name, reset, dim, d, reset)
				} else {
					fmt.Printf("               %s✓ %s%s\n", green, c.Name, reset)
				}
			}
		}
	}
	fmt.Println()
	if b.Done() {
		fmt.Printf("  %s%sEvery level unlocked. Holdfast is ready to ship.%s\n\n", bold, green, reset)
		return
	}
	fmt.Printf("  %sRestart the server after a change to see it on the site: go run ./cmd/holdfast%s\n\n", dim, reset)
}

func bar(pct int) string {
	const width = 10
	filled := pct * width / 100
	return "[" + strings.Repeat("■", filled) + strings.Repeat("·", width-filled) + "]"
}

func indent(s string) string {
	return strings.ReplaceAll(s, "\n", "\n             ")
}
