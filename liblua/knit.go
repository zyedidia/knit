package liblua

import (
	"fmt"
	"regexp"

	lua "github.com/zyedidia/knit/ktlua"
	luar "github.com/zyedidia/knit/ktluar"
)

func importKnit(L *lua.LState) *lua.LTable {
	pkg := L.NewTable()

	L.SetField(pkg, "Repl", luar.New(L, Repl))
	L.SetField(pkg, "ExtRepl", luar.New(L, ExtRepl))

	return pkg
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
