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
	lua "github.com/zyedidia/gopher-lua"
	"github.com/zyedidia/knit/rules"
)

// Flags for modifying the behavior of Knit.
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

// Flags that may be automatically set in a .knit.toml file.
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

// Capitalize the first rune of a string.
func title(s string) string {
	r, size := utf8.DecodeRuneInString(s)
	return string(unicode.ToTitle(r)) + s[size:]
}

// Returns true if 'path' exists.
func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

type assign struct {
	name  string
	value string
}

// Parses 'args' for expressions of the form 'key=value'. Assignments that are
// found are returned, along with the remaining arguments.
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

// Changes the working directory to 'dir' and changes all targets to be
// relative to that directory.
func goToKnitfile(vm *LuaVM, dir string, targets []string) error {
	wd, err := os.Getwd()
	if err != nil {
		return err
	}
	adir, err := filepath.Abs(dir)
	if err != nil {
		return err
	}
	for i, t := range targets {
		r, err := filepath.Rel(adir, wd)
		if err != nil {
			return err
		}
		if r != "" && r != "." {
			targets[i] = filepath.Join(r, t)
		}
	}

	return os.Chdir(dir)
}

var ErrNothingToDo = errors.New("nothing to be done")
var ErrQuiet = errors.New("quiet")

type ErrMessage struct {
	msg string
}

func (e *ErrMessage) Error() string {
	return e.msg
}

// Given the return value from the Lua evaluation of the Knitfile, returns all
// the buildsets and a list of the directories in order of priority. If the
// Knitfile requested to return an error (with a string), or quietly (with
// nil), returns an appropriate error.
func getBuildSets(lval lua.LValue) ([]string, map[string]*LBuildSet, error) {
	dirs := []string{"."}
	bsets := map[string]*LBuildSet{
		".": &LBuildSet{
			Dir:  ".",
			rset: LRuleSet{},
		},
	}

	addRuleSet := func(rs LRuleSet) {
		bsets["."].rset = append(bsets["."].rset, rs...)
	}
	addBuildSet := func(bs LBuildSet) {
		bsets[bs.Dir] = &bs
		dirs = append(dirs, bs.Dir)
	}

	switch v := lval.(type) {
	case lua.LString:
		return nil, nil, &ErrMessage{msg: string(v)}
	case *lua.LNilType:
		return nil, nil, ErrQuiet
	case *lua.LUserData:
		switch u := v.Value.(type) {
		case LRuleSet:
			addRuleSet(u)
		case LBuildSet:
			addBuildSet(u)
		default:
			return nil, nil, fmt.Errorf("invalid return value: %v", lval)
		}
	case *lua.LTable:
		v.ForEach(func(key, val lua.LValue) {
			if u, ok := val.(*lua.LUserData); ok {
				switch u := u.Value.(type) {
				case LRuleSet:
					addRuleSet(u)
				case LBuildSet:
					addBuildSet(u)
				}
			}
		})
	default:
		return nil, nil, fmt.Errorf("invalid return value: %v", lval)
	}
	return dirs, bsets, nil
}

func parseRuleSets(bsets map[string]*LBuildSet) (map[string]*rules.RuleSet, error) {
	rulesets := make(map[string]*rules.RuleSet)

	for k, v := range bsets {
		rs := rules.NewRuleSet()
		for _, lr := range v.rset {
			err := rules.ParseInto(lr.Contents, rs, lr.File, lr.Line)
			if err != nil {
				return nil, err
			}
		}
		rulesets[k] = rs
	}

	return rulesets, nil
}

// Run searches for a Knitfile and executes it, according to args (a list of
// targets and assignments), and the flags. All output is written to 'out'. The
// path of the executed knitfile is returned, along with a possible error.
func Run(out io.Writer, args []string, flags Flags) (string, error) {
	// change to the run dir
	if flags.RunDir != "" {
		os.Chdir(flags.RunDir)
	}

	vm := NewLuaVM()

	// make the cli and env tables containing the user variables and
	// environment variables
	cliAssigns, targets := makeAssigns(args)
	envAssigns, _ := makeAssigns(os.Environ())

	vm.MakeTable("cli", cliAssigns)
	vm.MakeTable("env", envAssigns)

	// find the build file by looking up from the current path
	file, dir, err := FindBuildFile(flags.Knitfile)
	if err != nil {
		return "", err
	}
	knitpath := filepath.Join(dir, file)
	if file == "" {
		// no build file found -- try to use the default one
		def, ok := DefaultBuildFile()
		if ok {
			file = def
		}
	} else if dir != "" {
		// found a knitfile in another directory, go to it
		err = goToKnitfile(vm, dir, targets)
		if err != nil {
			return knitpath, err
		}
	}

	if file == "" {
		return knitpath, fmt.Errorf("%s does not exist", flags.Knitfile)
	}

	// execute the knitfile
	lval, err := vm.DoFile(file)
	if err != nil {
		return knitpath, err
	}

	// get the build sets from the return value
	_, bsets, err := getBuildSets(lval)
	if err != nil {
		return knitpath, err
	}

	// parse them into rule sets
	rulesets, err := parseRuleSets(bsets)
	if err != nil {
		return knitpath, err
	}

	rs := rulesets["."]

	alltargets := rs.AllTargets()

	if len(targets) == 0 {
		targets = []string{rs.MainTarget()}
	}

	if len(targets) == 0 {
		return knitpath, errors.New("no targets")
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

	fmt.Println(rulesets)

	graph, err := rules.NewGraphSet(rulesets, ".", "_build", updated)
	if err != nil {
		return knitpath, err
	}

	// if graph.Size() == 1 {
	// 	return knitpath, fmt.Errorf("target not found: %s", strings.Join(targets, " "))
	// }

	err = graph.ExpandRecipes(vm)
	if err != nil {
		return knitpath, err
	}

	var db *rules.Database
	if flags.CacheDir == "." || flags.CacheDir == "" {
		db = rules.NewDatabase(".knit")
	} else {
		wd, err := os.Getwd()
		if err != nil {
			return knitpath, err
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
			return knitpath, fmt.Errorf("unknown tool: %s", flags.Tool)
		}

		return knitpath, t.Run(graph.Graph, flags.ToolArgs)
	}

	if flags.Ncpu <= 0 {
		return knitpath, errors.New("you must enable at least 1 core")
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
		return knitpath, err
	}
	if execerr != nil {
		return knitpath, execerr
	}
	if !rebuilt {
		return knitpath, fmt.Errorf("'%s': %w", strings.Join(targets, " "), ErrNothingToDo)
	}
	return knitpath, nil
}
