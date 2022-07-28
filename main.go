package main

import (
	"flag"
	"fmt"
	"log"
	"os"
)

func main() {
	makfile := flag.String("f", "makfile", "makfile to use")
	flag.Parse()

	args := flag.Args()
	if len(args) == 0 {
		log.Fatal("no target provided")
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
	e := NewExecutor(8, m, func(msg string) {
		fmt.Fprint(os.Stderr, msg)
	})
	e.ExecNode(g.base)
}
