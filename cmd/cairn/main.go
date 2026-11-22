// cairn is the command. M1's subcommands: init, put, edge, log, verify,
// version; M2's: query, current, each answered through the SQLite index
// beside the store, brought to the log's head first (sqlindex.Sync, D-35). Each one is specified in docs/SPEC.md first and does one thing;
// a refusal is written to stderr by name; stdout carries exactly one value
// per command (an identity, an id, a seq) so that a shell can compose them,
// and every note goes to stderr.
// Exit statuses: 0 answered, 1 the store refused (a verification or
// shape failure, named), 2 usage.
package main

import (
	"bufio"
	"flag"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"

	"forge.axonibyte.com/axonibyte/cairn/internal/cairn"
	"forge.axonibyte.com/axonibyte/cairn/internal/canon"
	"forge.axonibyte.com/axonibyte/cairn/internal/contract"
	"forge.axonibyte.com/axonibyte/cairn/internal/log"
	"forge.axonibyte.com/axonibyte/cairn/internal/query"
	"forge.axonibyte.com/axonibyte/cairn/internal/sqlindex"
	"forge.axonibyte.com/axonibyte/cairn/internal/store"
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr))
}

const usage = `cairn: a content-addressed store of relations (docs/SPEC.md)

  cairn init <root>                              make a store: keys, cairn.json, the founder's key-of edges
  cairn put <root> <file>|-                      store a blob (stdin with -), record wrote; stdout: the identity alone
  cairn edge <root> <rel> <s> [<o>] [k=v ...]    append a signed edge; stdout: the seq alone
  cairn concept <root> <name>                    mint a concept named by a literal; stdout: the id alone
  cairn log <root> [--from N]                    print the log's written forms from seq N
  cairn verify <root>                            verify every line, hash, signature and seal
  cairn query <root> <expr>                      answer a query of SPEC 11.3 at the log's head; stdout: one identity per line, sorted
  cairn current <root> <identity>                the current head of the identity's supersedes chain (5.4); stdout: the identity alone;
                                                 an identity no edge names is its own head -- a typo answers as itself, status 0
  cairn version
`

// run is main without the process: argv in, status out.
func run(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprint(stderr, usage)
		return 2
	}
	switch args[0] {
	case "version":
		fmt.Fprintf(stdout, "cairn %s (spec %s)\n", cairn.Version, cairn.SpecVersion)
		return 0
	case "init":
		if len(args) != 2 {
			fmt.Fprint(stderr, "cairn init <root>\n")
			return 2
		}
		s, err := store.Init(args[1], 0, 0, 0)
		if err != nil {
			fmt.Fprintf(stderr, "cairn init: %v\n", err)
			return 1
		}
		fmt.Fprintf(stdout, "%s\n%s\n", s.Writer(), s.Founder.Hash)
		return 0
	case "put":
		if len(args) != 3 {
			fmt.Fprint(stderr, "cairn put <root> <file>|-\n")
			return 2
		}
		s, err := store.Open(args[1])
		if err != nil {
			return refuse(stderr, "put", err)
		}
		var r io.Reader = stdin
		if args[2] != "-" {
			f, err := os.Open(args[2])
			if err != nil {
				fmt.Fprintf(stderr, "cairn put: %v\n", err)
				return 1
			}
			defer f.Close()
			r = f
		}
		id, seq, err := s.Put(r)
		if err != nil {
			return refuse(stderr, "put", err)
		}
		// stdout is one value, the identity, so `ID=$(cairn put ...)` composes
		// (quill, coop main/1008 finding 9); the rest is a note on stderr
		fmt.Fprintf(stdout, "%s\n", id)
		if seq == 0 {
			fmt.Fprintf(stderr, "cairn put: %s was already here; no edge added\n", id)
		} else {
			fmt.Fprintf(stderr, "cairn put: wrote recorded at seq %d\n", seq)
		}
		return 0
	case "edge":
		if len(args) < 4 {
			fmt.Fprint(stderr, "cairn edge <root> <rel> <s> [<o>] [k=v ...]\n")
			return 2
		}
		s, err := store.Open(args[1])
		if err != nil {
			return refuse(stderr, "edge", err)
		}
		rel, subject := args[2], args[3]
		object := ""
		rest := args[4:]
		if len(rest) > 0 && !strings.Contains(rest[0], "=") {
			object, rest = rest[0], rest[1:]
		}
		var attrs map[string]any
		for _, kv := range rest {
			k, v, ok := strings.Cut(kv, "=")
			if !ok || k == "" {
				fmt.Fprintf(stderr, "cairn edge: attribute %q is not k=v\n", kv)
				return 2
			}
			if attrs == nil {
				attrs = map[string]any{}
			}
			attrs[k] = attrValue(v)
		}
		seq, err := s.Edge(rel, subject, object, attrs)
		if err != nil {
			return refuse(stderr, "edge", err)
		}
		fmt.Fprintf(stdout, "%d\n", seq)
		return 0
	case "concept":
		if len(args) != 3 {
			fmt.Fprint(stderr, "cairn concept <root> <name>\n")
			return 2
		}
		s, err := store.Open(args[1])
		if err != nil {
			return refuse(stderr, "concept", err)
		}
		id, seq, err := s.NewConcept(args[2])
		if err != nil {
			return refuse(stderr, "concept", err)
		}
		fmt.Fprintf(stdout, "%s\n", id)
		fmt.Fprintf(stderr, "cairn concept: names recorded at seq %d\n", seq)
		return 0
	case "log":
		fs := flag.NewFlagSet("log", flag.ContinueOnError)
		fs.SetOutput(stderr)
		from := fs.Uint64("from", 1, "first seq to print")
		if len(args) < 2 {
			fmt.Fprint(stderr, "cairn log <root> [--from N]\n")
			return 2
		}
		if err := fs.Parse(args[2:]); err != nil {
			return 2
		}
		s, err := store.Open(args[1])
		if err != nil {
			return refuse(stderr, "log", err)
		}
		w := bufio.NewWriter(stdout)
		_, err = log.Read(s.Root, s.Founder, s.Config.SegmentBytes, *from, func(v log.Verified) error {
			b, err := canon.Canonical(v.Edge, true)
			if err != nil {
				return err
			}
			w.Write(b)
			return w.WriteByte('\n')
		})
		w.Flush()
		if err != nil {
			return refuse(stderr, "log", err)
		}
		return 0
	case "verify":
		if len(args) != 2 {
			fmt.Fprint(stderr, "cairn verify <root>\n")
			return 2
		}
		s, err := store.Open(args[1])
		if err != nil {
			return refuse(stderr, "verify", err)
		}
		n, seal, err := s.Verify()
		if err != nil {
			return refuse(stderr, "verify", err)
		}
		fmt.Fprintf(stdout, "verified %d edges; last seal at seq %d\n", n, seal)
		return 0
	case "query", "current":
		if len(args) != 3 {
			fmt.Fprintf(stderr, "cairn %s <root> <%s>\n", args[0], map[string]string{"query": "expr", "current": "identity"}[args[0]])
			return 2
		}
		s, err := store.Open(args[1])
		if err != nil {
			return refuse(stderr, args[0], err)
		}
		var q contractQuery
		if args[0] == "query" {
			// the grammar is checked before the index is touched, so a malformed
			// expression costs no rebuild
			q.q, err = query.Parse(args[2])
			if err != nil {
				return refuse(stderr, "query", err)
			}
		}
		head := s.Head()
		ix, err := syncIndex(s.Root, head, func(from uint64, fn func(log.Verified) error) error { return s.ReadLog(from, fn) })
		if err != nil {
			return refuse(stderr, args[0], err)
		}
		defer ix.Close()
		fmt.Fprintf(stderr, "index at seq %d\n", ix.At())
		if args[0] == "current" {
			head, err := ix.Current(args[2])
			if err != nil {
				return refuse(stderr, "current", err)
			}
			fmt.Fprintln(stdout, head)
			return 0
		}
		subjects, err := ix.QueryAt(q.q, head)
		if err != nil {
			return refuse(stderr, "query", err)
		}
		for _, id := range subjects {
			fmt.Fprintln(stdout, id)
		}
		return 0
	}
	fmt.Fprintf(stderr, "cairn: %q is not a command\n%s", args[0], usage)
	return 2
}

// syncIndex is sqlindex.Sync; a variable so that the test can hand the
// command an index short of the head and see QueryAt refuse it. Sync's
// postcondition makes that impossible in the binary, and QueryAt stays as
// the guard that would catch it if the premise ever broke (quill, coop
// main/1085: keep the guard, pin the premise).
var syncIndex = sqlindex.Sync

// contractQuery holds a parsed query so the switch above reads flat.
type contractQuery struct{ q contract.Query }

// refuse writes a named refusal and returns status 1.
func refuse(stderr io.Writer, cmd string, err error) int {
	fmt.Fprintf(stderr, "cairn %s: REFUSED: %v\n", cmd, err)
	return 1
}

// attrValue reads a k=v attribute's value as §4.2a admits: an integer, a
// boolean or null when it reads as one, else a string.
func attrValue(v string) any {
	if n, err := strconv.ParseInt(v, 10, 64); err == nil {
		return n
	}
	switch v {
	case "true":
		return true
	case "false":
		return false
	case "null":
		return nil
	}
	return v
}
