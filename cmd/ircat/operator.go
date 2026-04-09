package main

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/asabla/ircat/internal/auth"
	"github.com/asabla/ircat/internal/config"
	"github.com/asabla/ircat/internal/storage"
)

// runOperator dispatches the `ircat operator` subcommand
// family. The verbs the v1.2.x line ships are:
//
//	ircat operator add <username>     # mint or upsert
//	ircat operator list                # show all operators
//	ircat operator delete <username>   # remove
//
// All three open the same configured store the server uses,
// so an operator who locked themselves out of the dashboard
// can drop into the host and recover via the CLI without
// having to hand-craft an argon2id hash.
func runOperator(args []string) error {
	if len(args) == 0 {
		return operatorUsageError()
	}
	verb, rest := args[0], args[1:]
	switch verb {
	case "add":
		return runOperatorAdd(rest)
	case "list":
		return runOperatorList(rest)
	case "delete", "remove", "rm":
		return runOperatorDelete(rest)
	case "help", "-h", "--help":
		printOperatorUsage(os.Stderr)
		return flag.ErrHelp
	}
	return operatorUsageError()
}

func operatorUsageError() error {
	printOperatorUsage(os.Stderr)
	return fmt.Errorf("operator: unknown verb")
}

func printOperatorUsage(w io.Writer) {
	fmt.Fprintf(w, `usage: ircat operator <verb> [flags]

verbs:
  add <username>      mint a new operator (or upsert if it exists)
  list                show every persisted operator
  delete <username>   remove an operator

shared flags (every verb):
  --config <path>     path to the ircat config file (defaults to env IRCAT_CONFIG or %s)

flags for "add":
  --password-file <path>   read password from file instead of stdin
  --host-mask <mask>       restrict the operator to a hostmask (default: any)
  --flags <csv>            comma-separated flag list (e.g. kill,kline,rehash)

The "add" verb reads the password from stdin if --password-file
is not set. The password is hashed via argon2id before being
persisted; the plaintext never lives in the database.

A successful "add" upserts: an existing operator with the same
name is updated, with the new password hash + host mask + flag
set replacing the old.
`, defaultConfigPath())
}

// commonOperatorFlags is the shared --config flag every verb
// understands. Returns a parsed flag set so verb-specific
// helpers can layer their own flags on top.
func commonOperatorFlags(name string) (*flag.FlagSet, *string) {
	fs := flag.NewFlagSet("ircat operator "+name, flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	configPath := fs.String("config", defaultConfigPath(), "path to config file")
	return fs, configPath
}

// openOperatorStore loads the configured store the server
// would open at runtime. Used by every operator verb so the
// CLI hits the same persistence the dashboard reads from.
func openOperatorStore(ctx context.Context, configPath string) (storage.Store, *config.Config, error) {
	cfg, err := config.Load(configPath)
	if err != nil {
		return nil, nil, fmt.Errorf("load config %q: %w", configPath, err)
	}
	store, err := openStore(ctx, cfg)
	if err != nil {
		return nil, nil, fmt.Errorf("open storage: %w", err)
	}
	return store, cfg, nil
}

func runOperatorAdd(args []string) error {
	fs, configPath := commonOperatorFlags("add")
	passwordFile := fs.String("password-file", "", "read password from file (default: stdin)")
	hostMask := fs.String("host-mask", "", "operator hostmask, empty means any")
	flagsCSV := fs.String("flags", "all", "comma-separated flag list")

	fs.Usage = func() { printOperatorUsage(fs.Output()) }
	username, rest, err := extractPositional(args)
	if err != nil {
		printOperatorUsage(os.Stderr)
		return fmt.Errorf("operator add: %w", err)
	}
	if err := fs.Parse(rest); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	if fs.NArg() != 0 {
		printOperatorUsage(os.Stderr)
		return fmt.Errorf("operator add: unexpected extra arguments: %v", fs.Args())
	}

	password, err := readOperatorPassword(*passwordFile)
	if err != nil {
		return fmt.Errorf("read password: %w", err)
	}
	if password == "" {
		return fmt.Errorf("password is empty")
	}

	ctx := context.Background()
	store, cfg, err := openOperatorStore(ctx, *configPath)
	if err != nil {
		return err
	}
	defer store.Close()

	hash, err := auth.Hash(cfg.Auth.PasswordHash, password, auth.Argon2idParams{
		MemoryKiB:   uint32(cfg.Auth.Argon2id.MemoryKiB),
		Iterations:  uint32(cfg.Auth.Argon2id.Iterations),
		Parallelism: uint8(cfg.Auth.Argon2id.Parallelism),
	})
	if err != nil {
		return fmt.Errorf("hash password: %w", err)
	}
	op := &storage.Operator{
		Name:         username,
		HostMask:     *hostMask,
		PasswordHash: hash,
		Flags:        splitOperatorFlags(*flagsCSV),
	}

	// Upsert: try Create first, fall through to Update on
	// conflict. Same shape syncStaticOperators uses.
	if err := store.Operators().Create(ctx, op); err != nil {
		if !errors.Is(err, storage.ErrConflict) {
			return fmt.Errorf("create operator: %w", err)
		}
		if err := store.Operators().Update(ctx, op); err != nil {
			return fmt.Errorf("update operator: %w", err)
		}
		fmt.Fprintf(os.Stdout, "operator %q updated\n", username)
		return nil
	}
	fmt.Fprintf(os.Stdout, "operator %q created\n", username)
	return nil
}

func runOperatorList(args []string) error {
	fs, configPath := commonOperatorFlags("list")
	fs.Usage = func() { printOperatorUsage(fs.Output()) }
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	ctx := context.Background()
	store, _, err := openOperatorStore(ctx, *configPath)
	if err != nil {
		return err
	}
	defer store.Close()
	ops, err := store.Operators().List(ctx)
	if err != nil {
		return fmt.Errorf("list operators: %w", err)
	}
	if len(ops) == 0 {
		fmt.Fprintln(os.Stdout, "(no operators configured)")
		return nil
	}
	for _, op := range ops {
		mask := op.HostMask
		if mask == "" {
			mask = "*"
		}
		fmt.Fprintf(os.Stdout, "%s\thost=%s\tflags=%s\n",
			op.Name, mask, strings.Join(op.Flags, ","))
	}
	return nil
}

func runOperatorDelete(args []string) error {
	fs, configPath := commonOperatorFlags("delete")
	fs.Usage = func() { printOperatorUsage(fs.Output()) }
	username, rest, err := extractPositional(args)
	if err != nil {
		printOperatorUsage(os.Stderr)
		return fmt.Errorf("operator delete: %w", err)
	}
	if err := fs.Parse(rest); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	if fs.NArg() != 0 {
		printOperatorUsage(os.Stderr)
		return fmt.Errorf("operator delete: unexpected extra arguments: %v", fs.Args())
	}
	ctx := context.Background()
	store, _, err := openOperatorStore(ctx, *configPath)
	if err != nil {
		return err
	}
	defer store.Close()
	if err := store.Operators().Delete(ctx, username); err != nil {
		return fmt.Errorf("delete operator: %w", err)
	}
	fmt.Fprintf(os.Stdout, "operator %q deleted\n", username)
	return nil
}

// extractPositional consumes the leading non-flag token from
// args as the verb's positional argument and returns the
// remaining slice for the flag parser. The convention is
// "positional first, then flags":
//
//	ircat operator add alice --config /etc/ircat/config.yaml
//	ircat operator delete bob --config /etc/ircat/config.yaml
//
// This matches every existing irc daemon CLI and is what an
// operator types in muscle memory. We require this order
// (rather than letting flags come before the positional)
// because Go's stdlib `flag` package stops at the first
// non-flag, and figuring out whether a leading flag consumes
// the next token without introspecting the flag set is more
// complexity than the verb shape needs.
//
// Operators who want a positional that starts with `-` (e.g.
// a username called `--weird`) can use the `--` terminator:
//
//	ircat operator add -- --weird --config foo.yaml
func extractPositional(args []string) (string, []string, error) {
	if len(args) == 0 {
		return "", nil, fmt.Errorf("expected exactly one positional argument <username>")
	}
	if args[0] == "--" {
		if len(args) < 2 {
			return "", nil, fmt.Errorf("expected <username> after --")
		}
		return args[1], args[2:], nil
	}
	if strings.HasPrefix(args[0], "-") {
		return "", nil, fmt.Errorf("expected positional <username> as the first argument, got flag %q", args[0])
	}
	return args[0], args[1:], nil
}

// readOperatorPassword reads from path or stdin. Stdin is
// read line-by-line and stripped of the trailing newline.
// We deliberately do NOT use term.ReadPassword to avoid the
// extra dependency; operators who want a hidden prompt can
// pipe a password file or use shell redirection.
func readOperatorPassword(path string) (string, error) {
	if path != "" {
		data, err := os.ReadFile(path)
		if err != nil {
			return "", err
		}
		return strings.TrimRight(string(data), "\r\n"), nil
	}
	// Stdin path. Print a hint to stderr so an interactive
	// operator knows what to type, but only when stdin is a
	// tty (otherwise the hint pollutes the password when the
	// caller piped it in).
	if isTerminal(os.Stdin) {
		fmt.Fprint(os.Stderr, "password: ")
	}
	r := bufio.NewReader(os.Stdin)
	line, err := r.ReadString('\n')
	if err != nil && err != io.EOF {
		return "", err
	}
	return strings.TrimRight(line, "\r\n"), nil
}

// splitOperatorFlags parses the comma-separated --flags
// argument into a clean slice. Empty input returns nil so the
// resulting flag list does not contain a stray empty entry.
func splitOperatorFlags(s string) []string {
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// isTerminal reports whether f is connected to a terminal.
// Used by readOperatorPassword to decide whether to print the
// "password:" prompt. Implemented via the os.Stat mode bits so
// we do not need a third-party term package.
func isTerminal(f *os.File) bool {
	if f == nil {
		return false
	}
	st, err := f.Stat()
	if err != nil {
		return false
	}
	return (st.Mode() & os.ModeCharDevice) != 0
}
