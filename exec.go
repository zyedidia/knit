package main

import (
	"fmt"
	"os"
	"os/exec"
	"sync"
)

type Executor struct {
	Error     func(msg string)
	throttler chan struct{}
}

func NewExecutor(max int, e func(msg string)) *Executor {
	return &Executor{
		Error:     e,
		throttler: make(chan struct{}, max),
	}
}

func (e *Executor) ExecNode(n *node) {
	var wg sync.WaitGroup
	for _, p := range n.prereqs {
		wg.Add(1)
		e.throttler <- struct{}{}
		go func(pn *node) {
			defer wg.Done()
			e.ExecNode(pn)
			<-e.throttler
		}(p)
	}
	wg.Wait()

	for _, c := range n.rule.Recipe {
		e.ExecCommand(c)
	}
}

func (e *Executor) ExecCommand(c Command) {
	cmd := exec.Command(c.Name, c.Args...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	err := cmd.Run()
	if err != nil {
		e.Error(fmt.Sprintf("%v\n", err))
	}
}
