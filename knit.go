package knit

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
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
	Knitfile  string
	Ncpu      int
	Graph     string
	DryRun    bool
	RunDir    string
	Always    bool
	Quiet     bool
	ShowRules bool
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

func getRuleSets(vm *LuaVM, sets []string, rulesets map[string]*rules.RuleSet) error {
	for _, set := range sets {
		if _, ok := rulesets[set]; ok {
			continue
		}

		lrs, ok := vm.GetRuleSet(set)
		if !ok {
			return fmt.Errorf("ruleset not found: %s", set)
		}

		var sets []string
		rs := rules.NewRuleSet()
		for _, lr := range lrs.Rules {
			s, err := rules.ParseInto(lr.Contents, rs, lr.File, lr.Line)
			if err != nil {
				return err
			}
			sets = append(sets, s...)
		}
		rulesets[set] = rs
		err := getRuleSets(vm, sets, rulesets)
		if err != nil {
			return err
		}
	}
	return nil
}

var ErrNothingToDo = errors.New("nothing to be done")

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

	lval, err := vm.Eval(f, f.Name())
	if err != nil {
		return err
	}
	f.Close()

	rulesets := make(map[string]*rules.RuleSet)

	lruleset, ok := LToRuleSet(lval)
	if !ok {
		return fmt.Errorf("eval returned %s, expected ruleset", lval.Type())
	}
	err = getRuleSets(vm, []string{lruleset.name}, rulesets)
	if err != nil {
		return err
	}

	rs := rulesets[lruleset.name]

	if len(targets) == 0 {
		targets = []string{rs.MainTarget()}
	}

	if len(targets) == 0 {
		return errors.New("no targets")
	}

	rs.Add(rules.NewDirectRule([]string{":all"}, targets, nil, rules.AttrSet{
		Virtual: true,
		NoMeta:  true,
	}))

	graph, err := rules.NewGraphSet(rulesets, lruleset.name, ":all")
	if err != nil {
		return err
	}

	if graph.Size() == 1 {
		return fmt.Errorf("target not found: %s", targets)
	}

	if flags.Graph != "" {
		err := visualize(out, flags.Graph, graph)
		if err != nil {
			return err
		}
	}

	err = graph.ExpandRecipes(vm)
	if err != nil {
		return err
	}

	db := rules.NewDatabase(".knit")

	var w io.Writer = out
	if flags.Quiet {
		w = io.Discard
	}
	lock := sync.Mutex{}
	ex := rules.NewExecutor(".", db, flags.Ncpu, func(inputs, outputs, recipe []string, step, nsteps int) {
		// fmt.Printf("Building '%s' from '%s'\n", strings.Join(outputs, ", "), strings.Join(inputs, ", "))
	}, func(cmd, dir string) {
		lock.Lock()
		defer lock.Unlock()
		if dir != "." {
			fmt.Fprintf(w, "[in %s] ", dir)
		}
		fmt.Fprintln(w, cmd)
	}, func(msg string) {
		lock.Lock()
		defer lock.Unlock()
		fmt.Fprintln(out, msg)
	}, rules.Options{
		NoExec:       flags.DryRun,
		Shell:        "sh",
		AbortOnError: true,
		BuildAll:     flags.Always,
	})
	rebuilt, execerr := ex.Exec(graph.Graph)

	err = db.Save()
	if err != nil {
		return err
	}
	if execerr != nil {
		return execerr
	}
	if !rebuilt {
		return fmt.Errorf("'%s': %w", strings.Join(targets, " "), ErrNothingToDo)
	}
	return nil
}

func visualize(out io.Writer, file string, g *rules.GraphSet) error {
	var f io.Writer
	if file == "-" {
		f = out
	} else {
		fi, err := os.Create(file)
		if err != nil {
			return err
		}
		defer fi.Close()
		f = fi
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
