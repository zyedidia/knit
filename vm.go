package knit

import (
	"bytes"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"

	"github.com/gobwas/glob"
	"github.com/zyedidia/generic/stack"
	lua "github.com/zyedidia/gopher-lua"
	luar "github.com/zyedidia/gopher-luar"
	"github.com/zyedidia/knit/expand"
)

// A LuaVM tracks the Lua state and keeps a stack of directories that have been
// entered.
type LuaVM struct {
	L  *lua.LState
	wd *stack.Stack[string]
}

// An LRule is an un-parsed Lua representation of a build rule.
type LRule struct {
	Contents string
	File     string
	Line     int
}

func (r LRule) String() string {
	return "$ " + r.Contents
}

// An LRuleSet is a list of LRules.
type LRuleSet []LRule

func (rs LRuleSet) String() string {
	buf := &bytes.Buffer{}
	buf.WriteString("r{\n")
	for _, r := range rs {
		buf.WriteString(strings.TrimSpace(r.String()) + "\n")
	}
	buf.WriteByte('}')
	return buf.String()
}

// An LBuildSet is a list of rules associated with a directory.
type LBuildSet struct {
	Dir  string
	rset LRuleSet
}

func (bs *LBuildSet) String() string {
	buf := &bytes.Buffer{}
	buf.WriteString("b({\n")
	buf.WriteString(bs.rset.String())
	buf.WriteString("\n}, ")
	buf.WriteString(strconv.Quote(bs.Dir))
	buf.WriteByte(')')
	return buf.String()
}

// NewLuaVM constructs a new VM, and adds all the default built-ins.
func NewLuaVM() *LuaVM {
	L := lua.NewState(lua.Options{SkipOpenLibs: true})
	vm := &LuaVM{
		L:  L,
		wd: stack.New[string](),
	}
	vm.wd.Push(".")

	vm.OpenDefaults()
	vm.OpenKnit()

	rvar, rexpr := vm.ExpandFuncs()

	// Rules
	mkrule := func(rule string, file string, line int) LRule {
		// ignore errors during Lua-time rule expansion
		s, _ := expand.Expand(rule, rvar, rexpr)
		return LRule{
			Contents: s,
			File:     file,
			Line:     line,
		}
	}
	slcmt := luar.MT(L, []string{})
	L.SetField(slcmt.LTable, "__tostring", luar.New(L, func(s []string) string {
		return strings.Join(s, " ")
	}))
	rmt := luar.MT(L, LRule{})
	L.SetField(rmt.LTable, "__tostring", luar.New(L, func(r LRule) string {
		return r.String()
	}))
	L.SetGlobal("_rule", luar.New(L, mkrule))
	L.SetGlobal("rule", luar.New(L, func(rule string) LRule {
		dbg, ok := L.GetStack(1)
		file := "<rule>"
		line := 0
		if ok {
			L.GetInfo("nSl", dbg, nil)
			file = dbg.Source
			line = dbg.CurrentLine
		}
		return mkrule(rule, file, line)
	}))

	// Rule sets
	rsmt := luar.MT(L, LRuleSet{})
	L.SetField(rsmt.LTable, "__add", luar.New(L, func(r1, r2 LRuleSet) LRuleSet {
		rules := make(LRuleSet, len(r1)+len(r2))
		copy(rules, r1)
		copy(rules[len(r1):], r2)
		return rules
	}))
	L.SetField(rsmt.LTable, "__tostring", luar.New(L, func(rs LRuleSet) string {
		return rs.String()
	}))
	L.SetGlobal("r", luar.New(L, func(ruletbls ...[]LRule) LRuleSet {
		rules := make(LRuleSet, 0)
		for _, rs := range ruletbls {
			rules = append(rules, rs...)
		}
		return rules
	}))

	// Build sets
	bsmt := luar.MT(L, LBuildSet{})
	L.SetField(bsmt.LTable, "__tostring", luar.New(L, func(bs LBuildSet) string {
		return bs.String()
	}))

	L.SetGlobal("b", L.NewFunction(func(L *lua.LState) int {
		vals := L.ToTable(1)
		dir := L.OptString(2, ".")
		b := LBuildSet{}
		vals.ForEach(func(key lua.LValue, val lua.LValue) {
			switch v := val.(type) {
			case *lua.LUserData:
				switch u := v.Value.(type) {
				case LRuleSet:
					b.rset = append(b.rset, u...)
				case LRule:
					b.rset = append(b.rset, u)
				default:
					vm.Err(fmt.Errorf("invalid buildset item: %v", u))
				}
			default:
				vm.Err(fmt.Errorf("invalid buildset item: %v", v))
			}
		})
		b.Dir = filepath.Join(vm.Wd(), dir)
		L.Push(luar.New(L, b))
		return 1
	}))

	// Directory management
	L.SetGlobal("dcall", luar.New(L, func(fn *lua.LFunction, args ...lua.LValue) lua.LValue {
		var dbg lua.Debug
		_, err := L.GetInfo(">nSl", &dbg, fn)
		if err != nil {
			vm.Err(err)
			return lua.LNil
		}
		from := vm.EnterDir(filepath.Dir(dbg.Source))
		vm.L.Push(fn)
		for _, a := range args {
			vm.L.Push(a)
		}
		vm.L.Call(len(args), lua.MultRet)
		vm.LeaveDir(from)
		return vm.L.Get(-1)
	}))
	L.SetGlobal("rel", luar.New(L, func(files []string) []string {
		wd := vm.Wd()
		if wd == "." {
			return files
		}
		rels := make([]string, 0, len(files))
		for _, f := range files {
			rels = append(rels, filepath.Join(wd, f))
		}
		return rels
	}))

	// Include
	L.SetGlobal("include", luar.New(L, func(path string) lua.LValue {
		from := vm.EnterDir(filepath.Dir(path))
		val, err := vm.DoFile(filepath.Base(path))
		vm.LeaveDir(from)
		if err != nil {
			vm.Err(err)
			return nil
		}
		return val
	}))

	// Lua conversions
	L.SetGlobal("toarray", luar.New(L, func(v lua.LValue) lua.LValue {
		if v == nil || v.Type() == lua.LTNil {
			return v
		}
		switch v := v.(type) {
		case lua.LString:
			return luar.New(L, strings.Split(string(v), " "))
		}
		return v
	}))
	L.SetGlobal("tobool", luar.New(L, func(b lua.LValue) lua.LValue {
		// nil just passes through
		if b == nil || b.Type() == lua.LTNil {
			return b
		}
		switch v := b.(type) {
		case lua.LString:
			// a string becomes false if it is falsy, otherwise true
			switch v {
			case "false", "FALSE", "off", "OFF", "0":
				return lua.LFalse
			}
		case lua.LBool:
			// booleans remain the same
			return v
		}
		// anything else is true
		return lua.LTrue
	}))
	// TODO: should we override the default tostring and print?
	// L.SetGlobal("tostring", luar.New(L, func(v lua.LValue) string {
	// 	return ""
	// }))
	// L.SetGlobal("print", luar.New(L, func(v ...lua.LValue) {
	//
	// }))

	// Lua string formatting
	format := func(s string) string {
		s, err := expand.Expand(s, rvar, rexpr)
		if err != nil {
			vm.Err(err)
		}
		return s
	}
	// expand and throw an error if something is invalid
	L.SetGlobal("f", luar.New(L, format))
	L.SetGlobal("_format", luar.New(L, format))
	// expand without throwing an error for invalid expansions
	L.SetGlobal("expand", luar.New(L, func(s string) string {
		ret, _ := expand.Expand(s, rvar, rexpr)
		return ret
	}))

	// Lua eval
	L.SetGlobal("eval", luar.New(L, func(s string) lua.LValue {
		file := "<eval>"
		wd := vm.Wd()
		if wd != "." {
			file = filepath.Join(wd, file)
		}
		v, err := vm.Eval(strings.NewReader("return "+s), file)
		if err != nil {
			vm.Err(err)
			return lua.LNil
		}
		return v
	}))

	L.SetGlobal("use", luar.New(L, func(v *lua.LTable) {
		globals := L.GetGlobal("_G").(*lua.LTable)
		v.ForEach(func(key, val lua.LValue) {
			globals.RawSet(key, val)
		})
	}))

	return vm
}

// EnterDir changes into 'dir' and returns the path of the directory that was
// changed out of.
func (vm *LuaVM) EnterDir(dir string) string {
	wd, err := os.Getwd()
	if err != nil {
		vm.Err(err)
		return "."
	}
	vm.wd.Push(dir)
	os.Chdir(dir)
	return wd
}

// LeaveDir returns to the directory 'to' (usually the value returned by
// 'EnterDir').
func (vm *LuaVM) LeaveDir(to string) {
	vm.wd.Pop()
	os.Chdir(to)
}

// Wd returns the current working directory.
func (vm *LuaVM) Wd() string {
	return vm.wd.Peek()
}

// Err causes the VM to Lua-panic with 'err'.
func (vm *LuaVM) Err(err error) {
	vm.ErrStr(err.Error())
}

// ErrStr causes the VM to Lua-panic with a string message 'err'.
func (vm *LuaVM) ErrStr(err string) {
	vm.L.Error(lua.LString(err), 1)
}

// Eval runs the Lua code in 'r' with the filename 'file' and all local/global
// variables available in the current context. Returns the value that was
// generated, or a possible error.
func (vm *LuaVM) Eval(r io.Reader, file string) (lua.LValue, error) {
	if fn, err := vm.L.Load(r, file); err != nil {
		return nil, err
	} else {
		vm.L.SetFEnv(fn, getVars(vm.L))
		vm.L.Push(fn)
		err = vm.L.PCall(0, lua.MultRet, nil)
		if err != nil {
			return nil, err
		}
		return vm.L.Get(-1), nil
	}
}

// DoFile executes the Lua code inside 'file'. The file will be executed from
// the current directory, but the filename displayed for errors will be
// relative to the previous working directory.
func (vm *LuaVM) DoFile(file string) (lua.LValue, error) {
	f, err := os.Open(file)
	defer f.Close()
	if err != nil {
		return lua.LNil, err
	}
	if vm.Wd() != "." {
		file = filepath.Join(vm.Wd(), file)
	}
	if fn, err := vm.L.Load(f, file); err != nil {
		return nil, err
	} else {
		vm.L.Push(fn)
		err = vm.L.PCall(0, lua.MultRet, nil)
		if err != nil {
			return nil, err
		}
		return vm.L.Get(-1), nil
	}
}

// ExpandFuncs returns a set of functions used for expansion. The first expands
// by looking up variables in the current Lua context, and the second evaluates
// arbitrary Lua expressions.
func (vm *LuaVM) ExpandFuncs() (func(string) (string, error), func(string) (string, error)) {
	return func(name string) (string, error) {
			v := getVar(vm.L, name)
			if v == nil || v.Type() == lua.LTNil {
				return "", fmt.Errorf("expand: variable '%s' does not exist", name)
			}
			return LToString(v), nil
		}, func(expr string) (string, error) {
			v, err := vm.Eval(strings.NewReader("return "+expr), strconv.Quote(expr))
			if err != nil {
				return "", fmt.Errorf("expand: %w", err)
			} else if v == nil || v.Type() == lua.LTNil {
				return "nil", nil
			}
			return LToString(v), nil
		}
}

// OpenDefaults opens all default Lua libraries: package, base, table, debug,
// io, math, os, string.
func (vm *LuaVM) OpenDefaults() {
	for _, pair := range []struct {
		n string
		f lua.LGFunction
	}{
		{lua.LoadLibName, lua.OpenPackage}, // Must be first
		{lua.BaseLibName, lua.OpenBase},
		{lua.TabLibName, lua.OpenTable},
		{lua.DebugLibName, lua.OpenDebug},
		{lua.IoLibName, lua.OpenIo},
		{lua.MathLibName, lua.OpenMath},
		{lua.OsLibName, lua.OpenOs},
		{lua.StringLibName, lua.OpenString},
	} {
		if err := vm.L.CallByParam(lua.P{
			Fn:      vm.L.NewFunction(pair.f),
			NRet:    0,
			Protect: true,
		}, lua.LString(pair.n)); err != nil {
			panic(err)
		}
	}
}

// OpenKnit makes the 'knit' library available as a preloaded module.
func (vm *LuaVM) OpenKnit() {
	pkg := vm.pkgknit()
	loader := func(L *lua.LState) int {
		L.Push(pkg)
		return 1
	}
	vm.L.PreloadModule("knit", loader)
}

// Returns a table containing all values exposed as part of the 'knit' library.
func (vm *LuaVM) pkgknit() *lua.LTable {
	pkg := vm.L.NewTable()

	vm.L.SetField(pkg, "trim", luar.New(vm.L, strings.TrimSpace))
	vm.L.SetField(pkg, "os", luar.New(vm.L, runtime.GOOS))
	vm.L.SetField(pkg, "arch", luar.New(vm.L, runtime.GOARCH))
	vm.L.SetField(pkg, "glob", luar.New(vm.L, func(pattern string) []string {
		f, err := filepath.Glob(pattern)
		if err != nil {
			vm.Err(err)
		}
		if len(f) == 0 {
			return []string{}
		}
		return f
	}))
	vm.L.SetField(pkg, "rglob", luar.New(vm.L, func(path, pattern string) []string {
		g, err := glob.Compile(pattern)
		if err != nil {
			vm.Err(err)
			return nil
		}
		files := []string{}
		err = filepath.Walk(path, func(path string, info fs.FileInfo, err error) error {
			if err != nil {
				return err
			}
			if g.Match(info.Name()) {
				files = append(files, path)
			}
			return nil
		})
		if err != nil {
			vm.Err(err)
			return nil
		}
		return files
	}))
	vm.L.SetField(pkg, "abs", luar.New(vm.L, func(path string) string {
		p, err := filepath.Abs(path)
		if err != nil {
			vm.Err(err)
		}
		return p
	}))
	vm.L.SetField(pkg, "extrepl", luar.New(vm.L, func(in []string, ext, repl string) []string {
		patstr := fmt.Sprintf("%s$", regexp.QuoteMeta(ext))
		s, err := replace(in, patstr, repl)
		if err != nil {
			vm.Err(err)
		}
		return s
	}))
	vm.L.SetField(pkg, "repl", luar.New(vm.L, func(in []string, patstr, repl string) []string {
		s, err := replace(in, patstr, repl)
		if err != nil {
			vm.Err(err)
		}
		return s
	}))
	vm.L.SetField(pkg, "filterout", luar.New(vm.L, func(in []string, exclude []string) []string {
		removed := make([]string, 0, len(in))
		exmap := make(map[string]bool)
		for _, e := range exclude {
			exmap[e] = true
		}
		for _, s := range in {
			if !exmap[s] {
				removed = append(removed, s)
			}
		}
		return removed
	}))
	vm.L.SetField(pkg, "shell", luar.New(vm.L, func(shcmd string) string {
		cmd := exec.Command("sh", "-c", shcmd)
		b, err := cmd.Output()
		if err != nil {
			vm.Err(err)
		}
		return string(bytes.TrimSpace(b))
	}))
	return pkg
}

func replace(in []string, patstr, repl string) ([]string, error) {
	rgx, err := regexp.Compile(patstr)
	if err != nil {
		return nil, err
	}
	outs := make([]string, len(in))
	for i, v := range in {
		outs[i] = rgx.ReplaceAllString(v, repl)
	}
	return outs, nil
}

func addLocals(L *lua.LState, locals *lua.LTable) *lua.LTable {
	dbg, ok := L.GetStack(1)
	if ok {
		for j := 0; ; j++ {
			name, val := L.GetLocal(dbg, j)
			if val == nil || val.Type() == lua.LTNil {
				break
			}
			locals.RawSetString(name, val)
		}
	}
	return locals
}

func getLocals(L *lua.LState) *lua.LTable {
	locals := L.NewTable()
	return addLocals(L, locals)
}

func getVars(L *lua.LState) *lua.LTable {
	globals := L.GetGlobal("_G").(*lua.LTable)
	return addLocals(L, globals)
}

func getVar(L *lua.LState, v string) lua.LValue {
	dbg, ok := L.GetStack(1)
	if ok {
		for j := 0; ; j++ {
			name, val := L.GetLocal(dbg, j)
			if val == nil || val.Type() == lua.LTNil {
				break
			} else if name == v {
				return val
			}
		}
	}
	return L.GetGlobal(v)
}

// LToString converts a Lua value to a string.
func LToString(v lua.LValue) string {
	switch v := v.(type) {
	case *lua.LUserData:
		switch u := v.Value.(type) {
		case []string:
			return strings.Join(u, " ")
		default:
			return fmt.Sprintf("%v", u)
		}
	case *lua.LTable:
		if v.Len() == 0 {
			return LTableToString(v)
		}
		return LArrayToString(v)
	default:
		return fmt.Sprintf("%v", v)
	}
}

// LTableToString converts a Lua table to a string.
func LTableToString(v *lua.LTable) string {
	buf := &bytes.Buffer{}
	v.ForEach(func(k, v lua.LValue) {
		buf.WriteString(fmt.Sprintf("%s=%s", LToString(k), LToString(v)))
		buf.WriteByte(' ')
	})
	return buf.String()
}

// LArrayToString converts a Lua array (table with length) to a string.
func LArrayToString(v *lua.LTable) string {
	size := v.Len()
	buf := &bytes.Buffer{}
	i := 0
	v.ForEach(func(_, v lua.LValue) {
		buf.WriteString(LToString(v))
		if i < size-1 {
			buf.WriteByte(' ')
		}
		i++
	})
	return buf.String()
}

// MakeTable creates a global Lua table called 'name', with the key-value pairs
// from 'vals'.
func (vm *LuaVM) MakeTable(name string, vals []assign) {
	t := vm.L.NewTable()
	vm.L.SetGlobal(name, t)
	for _, a := range vals {
		vm.L.SetField(t, a.name, luar.New(vm.L, a.value))
	}
}
