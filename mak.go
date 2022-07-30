package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"runtime"
)

func main() {
	makfile := flag.String("f", "makfile", "makfile to use")
	ncpu := flag.Int("j", runtime.NumCPU(), "number of cores to use")
	flag.Parse()

	args := flag.Args()
	if len(args) == 0 {
		log.Fatal("no target provided")
	}

	if *ncpu <= 0 {
		log.Fatal("you must enable at least 1 core!")
	}

	target := args[0]

	data, err := os.ReadFile(*makfile)
	if err != nil {
		log.Fatal(err)
	}

	vm := newTclvm("rules")
	mak, err := vm.Eval(string(data))
	if err != nil {
		log.Fatal(err)
	}

	rvar, rexpr := expandFuncs(vm.itp)

	rs := parse(mak, *makfile, map[string][]string{}, errFns{
		printErr: func(e string) {
			fmt.Fprintln(os.Stderr, e)
		},
		errFn: func(e string) {
			fmt.Fprintln(os.Stderr, e)
			os.Exit(1)
		},
	}, expandFns{
		rvar:  rvar,
		rexpr: rexpr,
	})
	g, err := newGraph(rs, target)
	if err != nil {
		log.Fatalln(err)
	}
	e := newExecutor(*ncpu, vm, func(msg string) {
		fmt.Fprint(os.Stderr, msg)
	})
	e.execNode(g.base)
}
