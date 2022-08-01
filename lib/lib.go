package lib

import (
	"fmt"
	"os/exec"
	"path/filepath"
	"regexp"

	_ "embed"

	tcl "github.com/zyedidia/gotcl"
)

//go:embed lib.tcl
var lib string

func RegisterAll(interp *tcl.Interp) {
	register(interp, "glob", filepath.Glob)
	register(interp, "repl", ReplaceList)
	register(interp, "extrepl", ExtReplace)
	register(interp, "shell", Shell)

	_, err := interp.EvalString(lib)
	if err != nil {
		panic(err)
	}
}

func ExtReplace(in []string, ext, repl string) []string {
	patstr := fmt.Sprintf("%s$", regexp.QuoteMeta(ext))
	s, err := ReplaceList(in, patstr, repl)
	if err != nil {
		panic(err)
	}
	return s
}

func ReplaceList(in []string, patstr, repl string) ([]string, error) {
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

func Shell(shcmd string) (string, error) {
	cmd := exec.Command("sh", "-c", shcmd)
	b, err := cmd.Output()
	return string(b), err
}
