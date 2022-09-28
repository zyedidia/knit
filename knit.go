package knit

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"unicode"
	"unicode/utf8"

	"github.com/adrg/xdg"
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
	Clean    bool
	Style    string
	CacheDir string
	Hash     bool
	Commands bool
	Updated  []string
	Tool     string
	ToolArgs []string
}

type UserFlags struct {
	Knitfile *string
	Ncpu     *int
	Graph    *string
	DryRun   *bool
	RunDir   *string
	Always   *bool
	Quiet    *bool
	Clean    *bool
	Style    *string
	CacheDir *string
	Hash     *bool
	Commands *bool
	Updated  *[]string
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

	if exists(title(flags.Knitfile)) {
		flags.Knitfile = title(flags.Knitfile)
	}

	def, ok := DefaultBuildFile()
	if !exists(flags.Knitfile) && ok {
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

	alltargets := rs.AllTargets()

	if len(targets) == 0 {
		targets = []string{rs.MainTarget()}
	}

	if len(targets) == 0 {
		return errors.New("no targets")
	}

	rs.Add(rules.NewDirectRule([]string{"_build"}, targets, nil, rules.AttrSet{
		Virtual: true,
		NoMeta:  true,
		Rebuild: true,
	}))

	rs.Add(rules.NewDirectRule([]string{"_all"}, alltargets, nil, rules.AttrSet{
		Virtual: true,
		NoMeta:  true,
		Rebuild: true,
	}))

	updated := make(map[string]bool)
	for _, u := range flags.Updated {
		updated[u] = true
	}

	graph, err := rules.NewGraphSet(rulesets, lruleset.name, "_build", updated)
	if err != nil {
		return err
	}

	if graph.Size() == 1 {
		return fmt.Errorf("target not found: %s", targets)
	}

	err = graph.ExpandRecipes(vm)
	if err != nil {
		return err
	}

	var db *rules.Database
	if flags.CacheDir == "." || flags.CacheDir == "" {
		db = rules.NewDatabase(".knit")
	} else {
		wd, err := os.Getwd()
		if err != nil {
			return err
		}
		dir := flags.CacheDir
		if dir == "$cache" {
			dir = filepath.Join(xdg.CacheHome, "knit")
		}
		db = rules.NewCacheDatabase(dir, wd)
	}

	var w io.Writer = out
	if flags.Quiet {
		w = io.Discard
	}

	if flags.Tool != "" {
		var t rules.Tool
		switch flags.Tool {
		case "list":
			t = &rules.ListTool{}
		case "graph":
			t = &rules.GraphTool{W: w}
		case "clean":
			t = &rules.CleanTool{W: w, NoExec: flags.DryRun, All: flags.Always}
		case "rules":
			t = &rules.RulesTool{}
		case "targets":
			t = &rules.TargetsTool{}
		case "compdb":
			t = &rules.CompileDbTool{W: w}
		case "builddb":
			t = &rules.BuildDbTool{W: w}
		default:
			return fmt.Errorf("unknown tool: %s", flags.Tool)
		}

		return t.Run(graph.Graph, flags.ToolArgs)
	}

	if flags.Ncpu <= 0 {
		return errors.New("you must enable at least 1 core")
	}

	var printer rules.Printer
	switch flags.Style {
	case "steps":
		printer = &StepPrinter{w: w}
	case "progress":
		printer = &ProgressPrinter{
			w:     w,
			tasks: make(map[string]string),
		}
	default:
		printer = &BasicPrinter{w: w}
	}

	lock := sync.Mutex{}
	ex := rules.NewExecutor(".", db, flags.Ncpu, printer, func(msg string) {
		lock.Lock()
		fmt.Fprintln(out, msg)
		lock.Unlock()
	}, rules.Options{
		NoExec:       flags.DryRun,
		Shell:        "sh",
		AbortOnError: true,
		BuildAll:     flags.Always,
		Hash:         flags.Hash,
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
