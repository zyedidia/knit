package main

import (
	"bytes"
	"fmt"

	tcl "github.com/zyedidia/gotcl"
	"github.com/zyedidia/mak/expand"
)

type Machine struct {
	itp *tcl.Interp
	mak bytes.Buffer
}

func NewMachine(dsl string) *Machine {
	m := &Machine{
		itp: tcl.NewInterp(),
	}
	m.itp.SetCmd(dsl, func(itp *tcl.Interp, args []*tcl.TclObj) tcl.TclStatus {
		if len(args) != 1 {
			return itp.FailStr(fmt.Sprintf("%s: expected 1 argument, got %d", dsl, len(args)))
		}
		rvar, rexpr := expandFuncs(itp)
		output, err := expand.Expand(args[0].AsString(), rvar, rexpr)
		if err != nil {
			return itp.FailStr(fmt.Sprintf("%s: %s", dsl, err.Error()))
		}
		m.mak.WriteString(output)
		return 0
	})
	return m
}

func (m *Machine) Eval(s string) (string, error) {
	_, err := m.itp.EvalString(s)
	if err != nil {
		return "", err
	}
	return m.mak.String(), nil
}

func expandFuncs(itp *tcl.Interp) (func(string) (string, error), func(string) (string, error)) {
	return func(name string) (string, error) {
			v, err := itp.GetVarRaw(name)
			if err != nil {
				return "", err
			}
			return v.AsString(), nil
		}, func(expr string) (string, error) {
			v, err := itp.EvalString(expr)
			if err != nil {
				return "", err
			}
			return v.AsString(), nil
		}
}
