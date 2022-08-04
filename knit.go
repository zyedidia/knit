package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"strings"

	"github.com/zyedidia/knit/expand"
)

func main() {
	vm := NewLuaVM()

	flag.Parse()
	args := flag.Args()

	if len(args) == 0 {
		log.Fatal("no args")
	}

	f, err := os.Open(args[0])
	if err != nil {
		log.Fatal(err)
	}

	_, err = vm.Eval(f, f.Name())
	if err != nil {
		log.Fatal(err)
	}

	rvar, rexpr := vm.ExpandFuncs()
	for i, r := range vm.rules {
		s, err := expand.Expand(r, rvar, rexpr)
		if err != nil {
			log.Fatal(err)
		}
		vm.rules[i] = s
	}

	fmt.Println(strings.Join(vm.rules, "\n"))
}
