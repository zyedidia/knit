package knit

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"

	"github.com/zyedidia/knit/expand"
	lua "github.com/zyedidia/knit/ktlua"
	luar "github.com/zyedidia/knit/ktluar"
	"github.com/zyedidia/knit/liblua"
)

type LuaVM struct {
	L     *lua.LState
	rules []LRule

	vars map[string]*lua.LTable
}

type LRule struct {
	Contents string
	File     string
	Line     int
}

func NewLuaVM() *LuaVM {
	L := lua.NewState()
	vm := &LuaVM{
		L:    L,
		vars: make(map[string]*lua.LTable),
	}

	lib := liblua.FromLibs(liblua.Knit)
	L.SetGlobal("import", luar.New(L, func(pkg string) *lua.LTable {
		return lib.Import(L, pkg)
	}))
	L.SetGlobal("_rule", luar.New(L, func(rule string, file string, line int) {
		vm.rules = append(vm.rules, LRule{
			Contents: rule,
			File:     file,
			Line:     line,
		})
	}))
	L.SetGlobal("rule", luar.New(L, func(rule string) {
		vm.rules = append(vm.rules, LRule{
			Contents: rule,
			File:     "<rule>",
		})
	}))
	L.SetGlobal("tostring", luar.New(L, func(v lua.LValue) string {
		return LToString(v)
	}))
	L.SetGlobal("eval", luar.New(L, func(s string) lua.LValue {
		v, _ := vm.Eval(strings.NewReader("return "+s), "<eval>")
		return v
	}))

	fn := func(name string) (string, error) {
		v := getVar(L, name)
		if v == nil {
			return "", fmt.Errorf("f: variable '%s' does not exist", name)
		}
		return LToString(v), nil
	}
	L.SetGlobal("f", luar.New(L, func(s string) string {
		s, err := expand.Expand(s, fn, fn)
		if err != nil {
			fmt.Fprintf(os.Stderr, "warning: %v\n", err)
		}
		return s
	}))

	return vm
}

func getVar(L *lua.LState, v string) lua.LValue {
	i := 0
	for dbg, ok := L.GetStack(i); ok; dbg, ok = L.GetStack(i) {
		for j := 0; ; j++ {
			name, val := L.GetLocal(dbg, j)
			if val == nil || val.Type() == lua.LTNil {
				break
			} else if name == v {
				return val
			}
		}
		i++
	}
	return L.GetGlobal(v)
}

func (vm *LuaVM) Eval(r io.Reader, file string) (lua.LValue, error) {
	if fn, err := vm.L.Load(r, file); err != nil {
		return nil, err
	} else {
		vm.L.Push(fn)
		err := vm.L.PCall(0, lua.MultRet, nil)
		if err != nil {
			return nil, err
		}
		return vm.L.Get(-1), nil
	}
}

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

func LTableToString(v *lua.LTable) string {
	buf := &bytes.Buffer{}
	v.ForEach(func(k, v lua.LValue) {
		buf.WriteString(fmt.Sprintf("%s=%s", LToString(k), LToString(v)))
		buf.WriteByte(' ')
	})
	return buf.String()
}

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

func (vm *LuaVM) ExpandFuncs() (func(string) (string, error), func(string) (string, error)) {
	return func(name string) (string, error) {
			v := vm.L.GetGlobal(name)
			if v == nil || v.Type() == lua.LTNil {
				return "", fmt.Errorf("expand: variable '%s' does not exist", name)
			}
			return LToString(v), nil
		}, func(expr string) (string, error) {
			v, err := vm.Eval(strings.NewReader("return "+expr), strconv.Quote(expr))
			if err != nil {
				return "", fmt.Errorf("expand: %w", err)
			} else if v == nil || v.Type() == lua.LTNil {
				return "", fmt.Errorf("expand: '%s' did not return a value", expr)
			}
			return LToString(v), nil
		}
}

func (vm *LuaVM) SetVar(name string, val interface{}) {
	vm.L.SetGlobal(name, luar.New(vm.L, val))
}

func fromString(val string) interface{} {
	i, err := strconv.Atoi(val)
	if err == nil {
		return i
	}
	return val
}

func (vm *LuaVM) MakeTable(tbl string) {
	t := vm.L.NewTable()
	vm.L.SetGlobal(tbl, t)
	vm.vars[tbl] = t
}

func (vm *LuaVM) AddVar(tbl, name, val string) {
	if _, ok := vm.vars[tbl]; !ok {
		vm.MakeTable(tbl)
	}
	vm.L.SetField(vm.vars[tbl], name, luar.New(vm.L, fromString(val)))
}
