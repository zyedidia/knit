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

	m := NewMachine("rules")
	mak, err := m.Eval(string(data))
	if err != nil {
		log.Fatal(err)
	}

	rs := parse(mak, "makfile", *makfile, map[string][]string{}, ErrFns{
		PrintErr: func(e string) {
			fmt.Fprintln(os.Stderr, e)
		},
		Err: func(e string) {
			fmt.Fprintln(os.Stderr, e)
			os.Exit(1)
		},
	})
	g, err := newGraph(rs, target)
	if err != nil {
		log.Fatalln(err)
	}
	e := NewExecutor(*ncpu, m, func(msg string) {
		fmt.Fprint(os.Stderr, msg)
	})
	e.ExecNode(g.base)
}
