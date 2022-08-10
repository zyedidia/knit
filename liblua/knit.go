package liblua

import (
	"fmt"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"

	lua "github.com/zyedidia/knit/ktlua"
	luar "github.com/zyedidia/knit/ktluar"
)

func importKnit(L *lua.LState) *lua.LTable {
	pkg := L.NewTable()

	L.SetField(pkg, "repl", luar.New(L, Repl))
	L.SetField(pkg, "extrepl", luar.New(L, ExtRepl))
	L.SetField(pkg, "glob", luar.New(L, filepath.Glob))
	L.SetField(pkg, "shell", luar.New(L, Shell))
	L.SetField(pkg, "trim", luar.New(L, strings.TrimSpace))
	L.SetField(pkg, "abs", luar.New(L, Abs))

	return pkg
}

func Abs(path string) string {
	p, err := filepath.Abs(path)
	if err != nil {
		return err.Error()
	}
	return p
}

func ExtRepl(in []string, ext, repl string) []string {
	patstr := fmt.Sprintf("%s$", regexp.QuoteMeta(ext))
	s, err := Repl(in, patstr, repl)
	if err != nil {
		panic(err)
	}
	return s
}

func Repl(in []string, patstr, repl string) ([]string, error) {
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

func Shell(shcmd string) string {
	cmd := exec.Command("sh", "-c", shcmd)
	b, err := cmd.Output()
	if err != nil {
		return fmt.Sprintf("%v", err)
	}
	return string(b)
}
