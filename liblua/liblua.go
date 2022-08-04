package liblua

import (
	lua "github.com/zyedidia/knit/ktlua"
)

type Importer func(*lua.LState) *lua.LTable

type Lib map[string]Importer

func FromLibs(libs ...Lib) Lib {
	newl := make(map[string]Importer)

	for _, l := range libs {
		for k, v := range l {
			newl[k] = v
		}
	}
	return newl
}

var Go = Lib{
	"fmt":           importFmt,
	"io":            importIo,
	"io/ioutil":     importIoIoutil,
	"net":           importNet,
	"math":          importMath,
	"math/rand":     importMathRand,
	"os":            importOs,
	"os/exec":       importOsExec,
	"runtime":       importRuntime,
	"path":          importPath,
	"path/filepath": importPathFilepath,
	"strings":       importStrings,
	"regexp":        importRegexp,
	"errors":        importErrors,
	"time":          importTime,
	"unicode/utf8":  importUnicodeUtf8,
	"net/http":      importNetHttp,
	"archive/zip":   importArchiveZip,
}

var Knit = Lib{
	"knit": importKnit,
}

func (l Lib) Import(L *lua.LState, pkg string) *lua.LTable {
	if fn, ok := l[pkg]; ok {
		return fn(L)
	}
	return nil
}
