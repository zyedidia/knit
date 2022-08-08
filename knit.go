package knit

import (
	"fmt"
	"io"
	"os"
	"strings"

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

type Flags struct {
	Knitfile string
	Ncpu     int
	Viz      string
	DryRun   bool
	RunDir   string
	Always   bool
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

func Run(out io.Writer, args []string, flags Flags) {
	if flags.RunDir != "" {
		must(os.Chdir(flags.RunDir))
	}

	if flags.Ncpu <= 0 {
		fatal("you must enable at least 1 core")
	}

	if exists(strings.Title(flags.Knitfile)) {
		flags.Knitfile = strings.Title(flags.Knitfile)
	}

	def := DefaultBuildFile()
	if !exists(flags.Knitfile) && exists(def) {
		flags.Knitfile = def
	}

	f, err := os.Open(flags.Knitfile)
	must(err)

	vm := NewLuaVM()

	cliAssigns, targets := makeAssigns(args)
	envAssigns, _ := makeAssigns(os.Environ())

	vm.MakeTable("cli")
	for _, v := range cliAssigns {
		vm.AddVar("cli", v.name, v.value)
	}
	vm.MakeTable("env")
	for _, v := range envAssigns {
		vm.AddVar("env", v.name, v.value)
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
		must(rules.ParseInto(r.Contents, rs, fmt.Sprintf("%s:%d:<rule>", r.File, r.Line), errs, expands))
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

	if g.Size() == 1 {
		fatalf("target not found: %s", targets)
	}

	must(g.ExpandRecipes(vm))

	if flags.Viz != "" {
		f, err := os.Create(flags.Viz)
		must(err)
		g.Visualize(f)
		must(f.Close())
	}

	db := rules.NewDatabase(".knit")

	e := rules.NewExecutor(db, flags.Ncpu, out, rules.Options{
		NoExec:       flags.DryRun,
		Shell:        "sh",
		AbortOnError: true,
		BuildAll:     flags.Always,
	}, func(msg string) {
		fmt.Fprintln(os.Stderr, msg)
	})

	e.Exec(g)

	must(db.Save())
}
