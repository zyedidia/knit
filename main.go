package main

import (
	"flag"
	"fmt"
	"log"
	"os"
)

var rules = []Rule{
	{
		Targets: []Pattern{{"step1", nil}},
		Prereqs: []string{},
		Recipe: []Command{
			{"echo", []string{"step1"}},
		},
	},
	{
		Targets: []Pattern{{"step2", nil}},
		Prereqs: []string{"step1"},
		Recipe: []Command{
			{"echo", []string{"step2"}},
		},
	},
}

func main() {
	flag.Parse()
	args := flag.Args()

	rs := newRuleSet(rules...)
	g, err := newGraph(rs, args[0])
	if err != nil {
		log.Fatalln(err)
	}
	e := NewExecutor(8, func(msg string) {
		fmt.Fprint(os.Stderr, msg)
	})
	e.ExecNode(g.base)
}
