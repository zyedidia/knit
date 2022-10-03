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

var ErrNothingToDo = errors.New("nothing to be done")

func Run(out io.Writer, args []string, flags Flags) error {
	vm := NewLuaVM()

	cliAssigns, targets := makeAssigns(args)
	envAssigns, _ := makeAssigns(os.Environ())

	if flags.RunDir != "" {
		os.Chdir(flags.RunDir)
	}

	file, dir, err := FindBuildFile(flags.Knitfile)
	if err != nil {
		return err
	}
	if file == "" {
		def, ok := DefaultBuildFile()
		if ok {
			file = def
		}
	} else if dir != "" && flags.RunDir == "" {
		wd, err := os.Getwd()
		if err != nil {
			return err
		}
		for i, t := range targets {
			r, err := filepath.Rel(dir, wd)
			if err != nil {
				return err
			}
			if r != "" && r != "." {
				targets[i] = fmt.Sprintf("[%s]%s", r, t)
			}
		}

		os.Chdir(dir)
	}

	f, err := os.Open(file)
	if err != nil {
		return err
	}

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

	lsets, ok := LToRuleSets(lval)
	if !ok {
		return fmt.Errorf("eval returned %s, expected ruleset", lval.Type())
	}
	wd, _ := filepath.Abs(".")
	for _, lset := range lsets {
		rs := rules.NewRuleSet()
		for _, lr := range lset.Rules {
			_, err := rules.ParseInto(lr.Contents, rs, lr.File, lr.Line)
			if err != nil {
				return err
			}
			dir, _ := filepath.Rel(wd, lset.Dir)
			rulesets[dir] = rs
		}
	}

	rs := rulesets["."]

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

	graph, err := rules.NewGraphSet(rulesets, ".", "_build", updated)
	if err != nil {
		return err
	}

	if graph.Empty() {
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
			t = &rules.ListTool{W: w}
		case "graph":
			t = &rules.GraphTool{W: w}
		case "clean":
			t = &rules.CleanTool{W: w, NoExec: flags.DryRun, All: flags.Always}
		case "targets":
			t = &rules.TargetsTool{W: w}
		case "compdb":
			t = &rules.CompileDbTool{W: w}
		case "commands":
			t = &rules.CommandsTool{W: w}
		case "status":
			t = &rules.StatusTool{W: w, Db: db, Hash: flags.Hash}
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
