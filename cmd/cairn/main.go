// cairn is the command. At M0 it prints its version and says plainly what it
// does not yet do; the subcommands arrive with the milestones in
// docs/ROADMAP.md and every one of them is specified in docs/SPEC.md first.
package main

import (
	"fmt"
	"os"

	"forge.axonibyte.com/axonibyte/cairn/internal/cairn"
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

// run is main without the process: argv in, status out, so a test can drive
// it without exec. Exit statuses: 0 the request was answered, 2 usage.
func run(args []string, stdout, stderr *os.File) int {
	if len(args) == 0 || args[0] == "version" {
		fmt.Fprintf(stdout, "cairn %s (spec %s)\n", cairn.Version, cairn.SpecVersion)
		return 0
	}
	fmt.Fprintf(stderr, "cairn: %q is not a command yet; this build stores nothing (docs/ROADMAP.md M0). Try: cairn version\n", args[0])
	return 2
}
