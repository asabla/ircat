// Command ircat is the IRC server entrypoint.
//
// With no subcommand it runs the server: loads config, initializes
// logging, and waits for SIGINT/SIGTERM. The "healthcheck" subcommand
// is a tiny HTTP probe used by container orchestration. Future
// subcommands (token, oper, migrate) plug in via [dispatch].
package main

import (
	"errors"
	"flag"
	"fmt"
	"os"
)

// Build metadata, set via -ldflags at build time.
var (
	version = "dev"
	commit  = "unknown"
	date    = "unknown"
)

func main() {
	if err := dispatch(os.Args[1:]); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return
		}
		fmt.Fprintln(os.Stderr, "ircat:", err)
		os.Exit(1)
	}
}

// dispatch routes to a subcommand. The first non-flag argument is the
// subcommand; if there is none, runServer is the default. This means
// `ircat --config foo.yaml` and `ircat server --config foo.yaml` are
// equivalent, while `ircat healthcheck --address ...` runs the probe.
func dispatch(args []string) error {
	if len(args) > 0 {
		switch args[0] {
		case "server":
			return runServer(args[1:])
		case "healthcheck":
			return runHealthcheck(args[1:])
		case "operator":
			return runOperator(args[1:])
		case "version":
			fmt.Printf("ircat %s (commit %s, built %s)\n", version, commit, date)
			return nil
		case "help", "-h", "--help":
			printUsage(os.Stderr)
			return flag.ErrHelp
		}
	}
	return runServer(args)
}

func printUsage(w *os.File) {
	fmt.Fprintf(w, `usage: ircat [subcommand] [flags]

subcommands:
  server         run the IRC server (default)
  healthcheck    HTTP probe used by container orchestration
  operator       manage operator accounts (add, list, delete)
  version        print version and exit
  help           show this message

Run "ircat <subcommand> --help" for subcommand flags.
`)
}
