package tcllib

import (
	"path/filepath"

	tcl "github.com/zyedidia/gotcl"
)

func RegisterAll(interp *tcl.Interp) {
	register(interp, "glob", filepath.Glob)
}
