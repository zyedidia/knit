package main

import (
	"bytes"
	"fmt"

	tcl "github.com/zyedidia/gotcl"
	"github.com/zyedidia/take/lib"
)

type tclvm struct {
	itp   *tcl.Interp
	rules bytes.Buffer
}

func newTclvm(dsl string, file string) *tclvm {
	m := &tclvm{
		itp: tcl.NewInterp(),
	}
	m.itp.SetSource(file)
	m.itp.SetCmd(dsl, func(itp *tcl.Interp, args []*tcl.TclObj) tcl.TclStatus {
		if len(args) != 1 {
			return itp.FailStr(fmt.Sprintf("%s: expected 1 argument, got %d", dsl, len(args)))
		}
		m.rules.WriteString(args[0].AsString())
		return 0
	})
	lib.RegisterAll(m.itp)
	return m
}

func (m *tclvm) Eval(s string) (string, error) {
	_, err := m.itp.EvalString(s)
	if err != nil {
		return "", err
	}
	return m.rules.String(), nil
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
