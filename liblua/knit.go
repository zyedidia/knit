package liblua

import (
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"

	"github.com/gobwas/glob"
	lua "github.com/zyedidia/gopher-lua"
	luar "github.com/zyedidia/gopher-luar"
)

func importKnit(L *lua.LState) *lua.LTable {
	pkg := L.NewTable()

	L.SetField(pkg, "repl", luar.New(L, Repl))
	L.SetField(pkg, "extrepl", luar.New(L, ExtRepl))
	L.SetField(pkg, "glob", luar.New(L, Glob))
	L.SetField(pkg, "shell", luar.New(L, Shell))
	L.SetField(pkg, "trim", luar.New(L, strings.TrimSpace))
	L.SetField(pkg, "abs", luar.New(L, Abs))
	L.SetField(pkg, "os", luar.New(L, runtime.GOOS))
	L.SetField(pkg, "arch", luar.New(L, runtime.GOARCH))
	L.SetField(pkg, "readfile", luar.New(L, ReadFile))
	L.SetField(pkg, "join", luar.New(L, Join))
	L.SetField(pkg, "rglob", luar.New(L, Rglob))

	return pkg
}

func Join(a, b []string) []string {
	c := make([]string, 0, len(a)+len(b))
	c = append(c, a...)
	return append(c, b...)
}

func Glob(pattern string) []string {
	f, err := filepath.Glob(pattern)
	if err != nil {
		fmt.Fprintf(os.Stderr, "glob: %s\n", err)
		return nil
	}
	return f
}

func Rglob(path string, pattern string) []string {
	g, err := glob.Compile(pattern)
	if err != nil {
		fmt.Fprintf(os.Stderr, "rglob: %s\n", err)
		return nil
	}
	var files []string
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
		return nil
	}
	return files
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

func ReadFile(f string) lua.LValue {
	b, err := os.ReadFile(f)
	if err != nil {
		return lua.LNil
	}
	return lua.LString(string(b))
}
