package cli

import (
	"context"
	"flag"
	"fmt"
	"io"
	"sort"
	"strings"
)

// Command is a node in the command tree. Leaves have Run; branches have Sub.
type Command struct {
	Name  string
	Args  string // argument summary for usage, e.g. "<ref>"
	Short string
	Long  string
	Sub   []*Command
	Flags func(fs *flag.FlagSet)
	Run   func(ctx context.Context, app *App, args []string) error
}

func (c *Command) find(name string) *Command {
	for _, s := range c.Sub {
		if s.Name == name {
			return s
		}
	}
	return nil
}

// Dispatch walks argv down the command tree, parses flags, and runs the leaf.
func Dispatch(ctx context.Context, app *App, root *Command, argv []string) error {
	cmd := root
	path := []string{root.Name}

	for len(cmd.Sub) > 0 {
		// Global flags may appear before a subcommand name, e.g.
		// `tgl --json issue list` or `tgl issue --repo a/b list`. Split the
		// argv into flags and operands so the first operand is the subcommand.
		probe := flag.NewFlagSet("probe", flag.ContinueOnError)
		probe.SetOutput(io.Discard)
		app.registerGlobals(probe)
		leading, name, tail, err := splitBranch(probe, argv)
		if err != nil {
			usage(app.ErrOut, cmd, path)
			return Errorf(CodeError, "%v", err)
		}
		rest := append(append([]string{}, leading...), tail...)
		if name == "" {
			if hasHelp(argv) || len(argv) == 0 {
				usage(app.ErrOut, cmd, path)
				return nil
			}
			usage(app.ErrOut, cmd, path)
			return Errorf(CodeError, "%s: missing subcommand", strings.Join(path, " "))
		}
		next := cmd.find(name)
		if next == nil {
			usage(app.ErrOut, cmd, path)
			return Errorf(CodeError, "unknown command %q for %q", name, strings.Join(path, " "))
		}
		cmd, argv, path = next, rest, append(path, name)
	}

	fs := flag.NewFlagSet(strings.Join(path, " "), flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	app.registerGlobals(fs)
	if cmd.Flags != nil {
		cmd.Flags(fs)
	}
	fs.Usage = func() { usage(app.ErrOut, cmd, path) }

	if hasHelp(argv) {
		usage(app.ErrOut, cmd, path)
		return nil
	}
	// Reorder so that flags precede operands: the stdlib flag package stops
	// parsing at the first operand, which would silently drop `--patch` in
	// `tgl pr view 3ms4... --patch`.
	flags, operands := splitArgs(fs, argv)
	ordered := flags
	if len(operands) > 0 {
		ordered = append(append([]string{}, flags...), append([]string{"--"}, operands...)...)
	}
	if err := fs.Parse(ordered); err != nil {
		usage(app.ErrOut, cmd, path)
		return Errorf(CodeError, "%v", err)
	}
	if cmd.Run == nil {
		usage(app.ErrOut, cmd, path)
		return Errorf(CodeError, "%s: nothing to run", strings.Join(path, " "))
	}
	return cmd.Run(ctx, app, fs.Args())
}

func hasHelp(argv []string) bool {
	for _, a := range argv {
		if a == "-h" || a == "--help" || a == "help" {
			return true
		}
	}
	return false
}

func usage(w io.Writer, cmd *Command, path []string) {
	full := strings.Join(path, " ")
	if cmd.Short != "" {
		fmt.Fprintf(w, "%s\n\n", cmd.Short)
	}
	if len(cmd.Sub) > 0 {
		fmt.Fprintf(w, "Usage: %s <command> [flags]\n\nCommands:\n", full)
		subs := append([]*Command{}, cmd.Sub...)
		sort.Slice(subs, func(i, j int) bool { return subs[i].Name < subs[j].Name })
		width := 0
		for _, s := range subs {
			if len(s.Name) > width {
				width = len(s.Name)
			}
		}
		for _, s := range subs {
			fmt.Fprintf(w, "  %-*s  %s\n", width, s.Name, s.Short)
		}
	} else {
		args := cmd.Args
		if args != "" {
			args = " " + args
		}
		fmt.Fprintf(w, "Usage: %s%s [flags]\n", full, args)
		fs := flag.NewFlagSet(full, flag.ContinueOnError)
		fs.SetOutput(w)
		var app App
		app.registerGlobals(fs)
		if cmd.Flags != nil {
			cmd.Flags(fs)
		}
		fmt.Fprintf(w, "\nFlags:\n")
		fs.PrintDefaults()
	}
	if cmd.Long != "" {
		fmt.Fprintf(w, "\n%s\n", strings.TrimSpace(cmd.Long))
	}
}

// splitArgs partitions argv into flag tokens and operand tokens, consuming the
// value that follows a non-boolean flag.
//
// Unknown flags are kept in the flag group so that flag.Parse reports them as
// errors rather than having them silently become operands.
func splitArgs(fs *flag.FlagSet, argv []string) (flags, operands []string) {
	for i := 0; i < len(argv); i++ {
		a := argv[i]
		if a == "--" {
			operands = append(operands, argv[i+1:]...)
			return flags, operands
		}
		if len(a) < 2 || a[0] != '-' {
			operands = append(operands, a)
			continue
		}
		name := strings.TrimLeft(a, "-")
		if j := strings.IndexByte(name, '='); j >= 0 {
			// --flag=value carries its own value.
			flags = append(flags, a)
			continue
		}
		flags = append(flags, a)
		f := fs.Lookup(name)
		if f == nil {
			continue // unknown: let Parse complain
		}
		if bf, ok := f.Value.(interface{ IsBoolFlag() bool }); ok && bf.IsBoolFlag() {
			continue // booleans never take a separate value
		}
		if i+1 < len(argv) {
			i++
			flags = append(flags, argv[i])
		}
	}
	return flags, operands
}

// splitBranch scans the tokens that precede a subcommand name. Only global
// flags are meaningful there, so a flag the globals do not define is an error
// rather than something whose arity we would have to guess.
func splitBranch(globals *flag.FlagSet, argv []string) (leading []string, name string, tail []string, err error) {
	for i := 0; i < len(argv); i++ {
		a := argv[i]
		if a == "--" {
			if i+1 < len(argv) {
				return leading, argv[i+1], argv[i+2:], nil
			}
			return leading, "", nil, nil
		}
		if len(a) < 2 || a[0] != '-' {
			return leading, a, argv[i+1:], nil
		}
		flagName := strings.TrimLeft(a, "-")
		if j := strings.IndexByte(flagName, '='); j >= 0 {
			flagName = flagName[:j]
			if globals.Lookup(flagName) == nil {
				return nil, "", nil, fmt.Errorf("unknown flag %s before a subcommand", a)
			}
			leading = append(leading, a)
			continue
		}
		f := globals.Lookup(flagName)
		if f == nil {
			return nil, "", nil, fmt.Errorf("unknown flag %s before a subcommand", a)
		}
		leading = append(leading, a)
		if bf, ok := f.Value.(interface{ IsBoolFlag() bool }); ok && bf.IsBoolFlag() {
			continue
		}
		if i+1 < len(argv) {
			i++
			leading = append(leading, argv[i])
		}
	}
	return leading, "", nil, nil
}

// titleCase capitalises the first letter of an ASCII word.
func titleCase(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}
