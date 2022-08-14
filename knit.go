package knit

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/zyedidia/knit/rules"
)

func title(s string) string {
	r, size := utf8.DecodeRuneInString(s)
	return string(unicode.ToTitle(r)) + s[size:]
}

var Stderr io.Writer = os.Stderr

type Flags struct {
	Knitfile string
	Ncpu     int
	Graph    string
	DryRun   bool
	RunDir   string
	Always   bool
	Quiet    bool
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

func Run(out io.Writer, args []string, flags Flags) error {
	if flags.RunDir != "" {
		os.Chdir(flags.RunDir)
	}

	if flags.Ncpu <= 0 {
		return errors.New("you must enable at least 1 core")
	}

	if exists(title(flags.Knitfile)) {
		flags.Knitfile = title(flags.Knitfile)
	}

	def := DefaultBuildFile()
	if !exists(flags.Knitfile) && exists(def) {
		flags.Knitfile = def
	}

	f, err := os.Open(flags.Knitfile)
	if err != nil {
		return err
	}

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
	if err != nil {
		return err
	}

	f.Close()

	rs := rules.NewRuleSet()

	errs := rules.ErrFns{
		PrintErr: func(e string) {
			fmt.Fprint(Stderr, e)
		},
		Err: func(e string) {
			fmt.Fprintln(Stderr, e)
			os.Exit(1)
		},
	}

	rvar, rexpr := vm.ExpandFuncs()
	expands := rules.ExpandFns{
		Rvar:  rvar,
		Rexpr: rexpr,
	}

	for _, r := range vm.rules {
		err := rules.ParseInto(r.Contents, rs, fmt.Sprintf("%s:%d:<rule>", r.File, r.Line), errs, expands)
		if err != nil {
			return err
		}
	}

	if len(targets) == 0 {
		targets = rs.MainTargets()
	}

	if len(targets) == 0 {
		return errors.New("no targets")
	}

	rs.Add(rules.NewDirectRule([]string{":all"}, targets, nil, rules.AttrSet{
		Virtual: true,
		NoMeta:  true,
	}))

	g, err := rules.NewGraph(rs, ":all")
	if err != nil {
		return err
	}

	if g.Size() == 1 {
		return fmt.Errorf("target not found: %s", targets)
	}

	err = g.ExpandRecipes(vm)
	if err != nil {
		return err
	}

	if flags.Graph != "" {
		err := visualize(out, flags.Graph, g)
		if err != nil {
			return err
		}
	}

	db := rules.NewDatabase(".knit")

	e := rules.NewExecutor(db, flags.Ncpu, out, rules.Options{
		NoExec:       flags.DryRun,
		Shell:        "sh",
		AbortOnError: true,
		BuildAll:     flags.Always,
		Quiet:        flags.Quiet,
	}, func(msg string) {
		fmt.Fprintln(Stderr, msg)
	})

	err = e.Exec(g)
	if err != nil {
		return fmt.Errorf("'%s': %w", strings.Join(targets, " "), err)
	}

	return db.Save()
}

func visualize(out io.Writer, file string, g *rules.Graph) error {
	var f io.Writer
	if file == "-" {
		f = out
	} else {
		f, err := os.Create(file)
		if err != nil {
			return err
		}
		defer f.Close()
	}
	if strings.HasSuffix(file, ".dot") {
		g.VisualizeDot(f)
	} else if strings.HasSuffix(file, ".pdf") {
		buf := &bytes.Buffer{}
		g.VisualizeDot(buf)
		cmd := exec.Command("dot", "-Tpdf")
		cmd.Stdout = f
		cmd.Stdin = buf
		cmd.Stderr = Stderr
		err := cmd.Run()
		if err != nil {
			return err
		}
	} else {
		g.VisualizeText(f)
	}
	return nil
}
