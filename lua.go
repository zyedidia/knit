package main

import (
	"bytes"
	"fmt"
	"io"
	"strconv"
	"strings"

	lua "github.com/zyedidia/knit/ktlua"
	luar "github.com/zyedidia/knit/ktluar"
	"github.com/zyedidia/knit/liblua"
)

type LuaVM struct {
	L     *lua.LState
	rules []string
}

func NewLuaVM() *LuaVM {
	L := lua.NewState()
	vm := &LuaVM{
		L: L,
	}

	lib := liblua.FromLibs(liblua.Go, liblua.Knit)
	L.SetGlobal("import", luar.New(L, func(pkg string) *lua.LTable {
		return lib.Import(L, pkg)
	}))
	L.SetGlobal("_rule", luar.New(L, func(rule string) {
		vm.rules = append(vm.rules, rule)
	}))
	L.SetGlobal("tostring", luar.New(L, func(v lua.LValue) string {
		return LToString(v)
	}))

	return vm
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
			v, err := vm.Eval(strings.NewReader("return "+expr), fmt.Sprintf("%s", strconv.Quote(expr)))
			if err != nil {
				return "", fmt.Errorf("expand: %w", err)
			} else if v == nil || v.Type() == lua.LTNil {
				return "", fmt.Errorf("expand: %s did not return a value", strconv.Quote(expr))
			}
			return LToString(v), nil
		}
}
