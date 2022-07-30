package tcllib

import (
	"path/filepath"

	_ "embed"

	tcl "github.com/zyedidia/gotcl"
)

//go:embed lib.tcl
var lib string

func RegisterAll(interp *tcl.Interp) {
	register(interp, "glob", filepath.Glob)

	_, err := interp.EvalString(lib)
	if err != nil {
		panic(err)
	}
}
