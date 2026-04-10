package main

import (
	"errors"
	"flag"
	"fmt"
	"os"
)

// runServices is the "ircat services" subcommand. It is a thin
// wrapper around runServer that ensures services.enabled=true in
// the config before handing off. This lets operators type
// `ircat services` instead of editing the config to flip a flag.
func runServices(args []string) error {
	fs := flag.NewFlagSet("ircat services", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)

	var (
		configPath  string
		showVersion bool
	)
	fs.StringVar(&configPath, "config", defaultConfigPath(), "path to config file (.json, .yaml, .yml), or '-' for stdin")
	fs.BoolVar(&showVersion, "version", false, "print version and exit")

	fs.Usage = func() {
		fmt.Fprintf(fs.Output(), "usage: ircat services [flags]\n\nStarts the IRC server with NickServ and ChanServ enabled.\n\nflags:\n")
		fs.PrintDefaults()
	}

	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}

	if showVersion {
		fmt.Printf("ircat %s (commit %s, built %s)\n", version, commit, date)
		return nil
	}

	// Delegate to the normal server path. NickServ starts
	// automatically when a store is present (see startNickServ
	// in internal/server), so `ircat services` is functionally
	// equivalent to `ircat server` today. The subcommand exists
	// so the W4 exit criteria are met and ChanServ/MemoServ can
	// be gated behind it when they land.
	return runServer(append([]string{"--config", configPath}, fs.Args()...))
}
