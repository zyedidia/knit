package main

import (
	"flag"
	"fmt"
	"log"
	"os"
)

var rules = []Rule{
	{
		Targets: []Pattern{{false, "step1", nil}},
		Prereqs: []string{},
		Recipe: []Command{
			{"echo", []string{"step1"}},
		},
	},
	{
		Targets: []Pattern{{false, "step2", nil}},
		Prereqs: []string{"step1"},
		Recipe: []Command{
			{"echo", []string{"step2"}},
		},
	},
}

func main() {
	mainMak()
}

func mainMak() {
	m := NewMachine("rules")
	target := flag.String("target", "all", "target to build")

	flag.Parse()
	args := flag.Args()
	if len(args) == 0 {
		log.Fatal("no input")
	}

	file := args[0]
	data, err := os.ReadFile(file)
	if err != nil {
		log.Fatal(err)
	}

	mak, err := m.Eval(string(data))
	if err != nil {
		log.Fatal(err)
	}

	rs := parse(mak, "makfile", file, map[string][]string{}, ErrFns{
		PrintErr: func(e string) {
			fmt.Fprintln(os.Stderr, e)
		},
		Err: func(e string) {
			fmt.Fprintln(os.Stderr, e)
			os.Exit(1)
		},
	})
	g, err := newGraph(rs, *target)
	if err != nil {
		log.Fatalln(err)
	}
	e := NewExecutor(8, func(msg string) {
		fmt.Fprint(os.Stderr, msg)
	})
	e.ExecNode(g.base)
}

func mainTcl() {
	m := NewMachine("rules")

	flag.Parse()
	args := flag.Args()
	if len(args) == 0 {
		log.Fatal("no input")
	}

	file := args[0]
	data, err := os.ReadFile(file)
	if err != nil {
		log.Fatal(err)
	}

	rules, err := m.Eval(string(data))
	if err != nil {
		log.Fatal(err)
	}
	fmt.Print(rules)
}

func mainLex() {
	flag.Parse()
	args := flag.Args()
	if len(args) == 0 {
		log.Fatal("no input")
	}

	file := args[0]
	data, err := os.ReadFile(file)
	if err != nil {
		log.Fatal(err)
	}
	l := lex(string(data))

	for t := l.nextToken(); t.typ != tokenEnd; t = l.nextToken() {
		fmt.Println(t)
	}
}

func mainRules() {
	flag.Parse()
	args := flag.Args()

	target := rules[0].Targets[0].str
	if len(args) > 0 {
		target = args[0]
	}

	rs := newRuleSet(rules...)
	g, err := newGraph(rs, target)
	if err != nil {
		log.Fatalln(err)
	}
	e := NewExecutor(8, func(msg string) {
		fmt.Fprint(os.Stderr, msg)
	})
	e.ExecNode(g.base)
}
