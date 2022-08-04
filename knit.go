package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/zyedidia/knit/expand"
)

func fatalf(format string, args ...interface{}) {
	fmt.Fprintf(os.Stderr, format, args...)
	fmt.Fprintln(os.Stderr)
	os.Exit(1)
}

func fatal(s string) {
	fmt.Fprintln(os.Stderr, s)
	os.Exit(1)
}

func must(err error) {
	if err != nil {
		fatalf("%v", err)
	}
}

func assert(b bool) {
	if !b {
		panic("assertion failed")
	}
}

func main() {
	vm := NewLuaVM()

	flag.Parse()
	args := flag.Args()

	if len(args) == 0 {
		fatal("no arguments")
	}

	f, err := os.Open(args[0])
	must(err)

	_, err = vm.Eval(f, f.Name())
	must(err)

	rvar, rexpr := vm.ExpandFuncs()
	for _, r := range vm.rules {
		s, err := expand.Expand(r.Contents, rvar, rexpr)
		if err != nil {
			fatalf("%s:%d: in rule: %v", r.File, r.Line, err)
		}
		fmt.Println(s)
	}
}
