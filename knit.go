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
	Knitfile  string
	Ncpu      int
	DryRun    bool
	RunDir    string
	Always    bool
	Quiet     bool
	Style     string
	CacheDir  string
	Hash      bool
	Updated   []string
	Root      bool
	Shell     string
	KeepGoing bool
	Tool      string
	ToolArgs  []string
}

// Flags that may be automatically set in a .knit.toml file.
type UserFlags struct {
	Knitfile  *string
	Ncpu      *int
	DryRun    *bool
	RunDir    *string `toml:"directory"`
	Always    *bool
	Quiet     *bool
	Style     *string
	CacheDir  *string `toml:"cache"`
	Hash      *bool
	Updated   *[]string
	Root      *bool
	Shell     *string
	KeepGoing *bool
}

// Capitalize the first rune of a string.
func title(s string) string {
	r, size := utf8.DecodeRuneInString(s)
	return string(unicode.ToTitle(r)) + s[size:]
}

func rel(basepath, targpath string) (string, error) {
	slash := strings.HasSuffix(targpath, "/")
	rel, err := filepath.Rel(basepath, targpath)
	if err != nil {
		return rel, err
	}
	if slash {
		rel += "/"
	}
	return rel, err
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

func pathJoin(dir, target string) string {
	if filepath.IsAbs(target) {
		return target
	}
	p := filepath.Join(dir, target)
	if strings.HasSuffix(target, "/") {
		p += "/"
	}
	return p
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
		r, err := rel(adir, wd)
		if err != nil {
			return err
		}
		if r != "" && r != "." {
			targets[i] = pathJoin(r, t)
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
		".": {
			Dir:  ".",
			rset: LRuleSet{},
		},
	}

	var addBuildSet func(bs LBuildSet)
	addBuildSet = func(bs LBuildSet) {
		if b, ok := bsets[bs.Dir]; ok {
			b.rset = append(b.rset, bs.rset...)
		} else {
			bsets[bs.Dir] = &bs
			dirs = append(dirs, bs.Dir)
		}

		// TODO: can there be a buildset cycle?
		for _, bset := range bs.bsets {
			addBuildSet(bset)
		}
	}

	switch v := lval.(type) {
	case lua.LString:
		return nil, nil, &ErrMessage{msg: string(v)}
	case *lua.LNilType:
		return nil, nil, ErrQuiet
	case *lua.LUserData:
		switch u := v.Value.(type) {
		case LBuildSet:
			addBuildSet(u)
		default:
			return nil, nil, fmt.Errorf("invalid return value: %v", lval)
		}
	default:
		return nil, nil, fmt.Errorf("invalid return value: %v", lval)
	}
	return dirs, bsets, nil
}

// Run searches for a Knitfile and executes it, according to args (a list of
// targets and assignments), and the flags. All output is written to 'out'. The
// path of the executed knitfile is returned, along with a possible error.
func Run(out io.Writer, args []string, flags Flags) (string, error) {
	if flags.RunDir != "" {
		err := os.Chdir(flags.RunDir)
		if err != nil {
			return "", err
		}
	}

	vm := NewLuaVM(flags.Shell)

	cliAssigns, targets := makeAssigns(args)
	envAssigns, _ := makeAssigns(os.Environ())

	vm.MakeTable("cli", cliAssigns)
	vm.MakeTable("env", envAssigns)

	file, dir, err := FindBuildFile(flags.Knitfile)
	if err != nil {
		return "", err
	}
	knitpath := filepath.Join(dir, file)
	if file == "" {
		def, ok := DefaultBuildFile()
		if ok {
			file = def
		}
	} else if dir != "" {
		for i, u := range flags.Updated {
			p, err := filepath.Rel(dir, u)
			if err != nil {
				return knitpath, err
			}
			flags.Updated[i] = p
		}
		if flags.Root {
			err := os.Chdir(dir)
			if err != nil {
				return knitpath, err
			}
		} else {
			err = goToKnitfile(vm, dir, targets)
			if err != nil {
				return knitpath, err
			}
		}
	}

	if file == "" {
		return knitpath, fmt.Errorf("%s does not exist", flags.Knitfile)
	}

	lval, err := vm.DoFile(file)
	if err != nil {
		return knitpath, err
	}

	dirs, bsets, err := getBuildSets(lval)
	if err != nil {
		return knitpath, err
	}

	rulesets := make(map[string]*rules.RuleSet)

	for k, v := range bsets {
		rs := rules.NewRuleSet()
		for _, lr := range v.rset {
			err := rules.ParseInto(lr.Contents, rs, lr.File, lr.Line)
			if err != nil {
				return knitpath, err
			}
		}
		rulesets[k] = rs
	}

	rs := rulesets["."]

	var alltargets []string

	for rdir, rset := range rulesets {
		targets := rset.AllTargets()
		for _, t := range targets {
			alltargets = append(alltargets, pathJoin(rdir, t))
		}
	}

	if len(targets) == 0 {
		targets = []string{rs.MainTarget()}
	}
	// TODO: don't turn an empty target into '.'

	if len(targets) == 0 {
		return knitpath, errors.New("no targets")
	}

	rs.Add(rules.NewDirectRule([]string{":build"}, targets, nil, rules.AttrSet{
		Virtual: true,
		NoMeta:  true,
		Rebuild: true,
	}))

	rs.Add(rules.NewDirectRule([]string{":all"}, alltargets, nil, rules.AttrSet{
		Virtual: true,
		NoMeta:  true,
		Rebuild: true,
	}))

	updated := make(map[string]bool)
	for _, u := range flags.Updated {
		updated[u] = true
	}

	graph, err := rules.NewGraph(rulesets, dirs, ":build", updated)
	if err != nil {
		return knitpath, err
	}

	err = graph.ExpandRecipes(vm)
	if err != nil {
		return knitpath, err
	}

	var db *rules.Database
	if flags.CacheDir == "." || flags.CacheDir == "" {
		db = rules.NewDatabase(filepath.Join(".knit", file))
	} else {
		wd, err := os.Getwd()
		if err != nil {
			return knitpath, err
		}
		dir := flags.CacheDir
		if dir == "$cache" {
			dir = filepath.Join(xdg.CacheHome, "knit")
		}
		db = rules.NewCacheDatabase(dir, filepath.Join(wd, file))
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

		return knitpath, t.Run(graph, flags.ToolArgs)
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
		Shell:        flags.Shell,
		AbortOnError: !flags.KeepGoing,
		BuildAll:     flags.Always,
		Hash:         flags.Hash,
	})

	rebuilt, execerr := ex.Exec(graph)

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
