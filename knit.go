package main

import (
	"fmt"
	"io"
	"os"
	"runtime"
	"strings"

	"github.com/spf13/pflag"
	"github.com/zyedidia/knit/info"
	"github.com/zyedidia/knit/rules"
)

func fatalf(format string, args ...interface{}) {
	fmt.Fprint(os.Stderr, "knit: ")
	fmt.Fprintf(os.Stderr, format, args...)
	fmt.Fprintln(os.Stderr)
	os.Exit(1)
}

func fatal(s string) {
	fmt.Fprint(os.Stderr, "knit: ")
	fmt.Fprintln(os.Stderr, s)
	os.Exit(1)
}

func must(err error) {
	if err != nil {
		fatalf("%v", err)
	}
}

func assert(b bool) {
	if !b {
		panic("assertion failed")
	}
}

var flags = struct {
	knitfile *string
	ncpu     *int
	viz      *string
	dryrun   *bool
	rundir   *string
	always   *bool
	quiet    *bool
	version  *bool
}{
	knitfile: pflag.StringP("file", "f", "Knitfile", "Knitfile to use"),
	ncpu:     pflag.IntP("threads", "j", runtime.NumCPU(), "number of cores to use"),
	viz:      pflag.String("viz", "", "emit a graphiz file"),
	dryrun:   pflag.BoolP("dry-run", "n", false, "print commands without actually executing"),
	rundir:   pflag.StringP("directory", "C", "", "run command from directory"),
	always:   pflag.BoolP("always-build", "B", false, "unconditionally build all targets"),
	quiet:    pflag.BoolP("quiet", "q", false, "don't print commands"),
	version:  pflag.BoolP("version", "v", false, "show version information"),
}

type assign struct {
	name  string
	value string
}

func makeAssigns(args []string) ([]assign, []string) {
	assigns := make([]assign, 0, len(args))
	other := make([]string, 0)
	for _, a := range args {
		before, after, found := strings.Cut(a, "=")
		if found {
			assigns = append(assigns, assign{
				name:  before,
				value: after,
			})
		} else {
			other = append(other, a)
		}
	}
	return assigns, other
}

func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func main() {
	pflag.Parse()

	if *flags.version {
		fmt.Println("knit version", info.Version)
		os.Exit(0)
	}

	if *flags.rundir != "" {
		must(os.Chdir(*flags.rundir))
	}

	if *flags.ncpu <= 0 {
		fatal("you must enable at least 1 core")
	}

	if exists(strings.Title(*flags.knitfile)) {
		*flags.knitfile = strings.Title(*flags.knitfile)
	}

	args := pflag.Args()

	env := os.Environ()
	assigns, targets := makeAssigns(append(args, env...))

	f, err := os.Open(*flags.knitfile)
	must(err)

	vm := NewLuaVM()

	for _, v := range assigns {
		vm.SetVar(v.name, v.value)
	}

	_, err = vm.Eval(f, f.Name())
	must(err)

	must(f.Close())

	rs := rules.NewRuleSet()

	errs := rules.ErrFns{
		PrintErr: func(e string) {
			fmt.Fprint(os.Stderr, e)
		},
		Err: func(e string) {
			fatalf(e)
		},
	}

	rvar, rexpr := vm.ExpandFuncs()
	expands := rules.ExpandFns{
		Rvar:  rvar,
		Rexpr: rexpr,
	}

	for _, r := range vm.rules {
		rules.ParseInto(r.Contents, rs, fmt.Sprintf("%s:%d:<rule>", r.File, r.Line), errs, expands)
	}

	if len(targets) == 0 {
		targets = rs.MainTargets()
	}

	if len(targets) == 0 {
		fatal("no targets")
	}

	rs.Add(rules.NewDirectRule([]string{":all"}, targets, nil, rules.AttrSet{
		Virtual: true,
		NoMeta:  true,
	}))

	g, err := rules.NewGraph(rs, ":all")
	must(err)

	err = g.ExpandRecipes(vm)
	must(err)

	if *flags.viz != "" {
		f, err := os.Create(*flags.viz)
		must(err)
		g.Visualize(f)
		must(f.Close())
	}

	var out io.Writer
	if *flags.quiet {
		out = io.Discard
	} else {
		out = os.Stdout
	}

	e := rules.NewExecutor(*flags.ncpu, out, rules.Options{
		NoExec:       *flags.dryrun,
		Shell:        "sh",
		AbortOnError: true,
	}, func(msg string) {
		fmt.Fprintln(os.Stderr, msg)
	})

	e.Exec(g)
}
