package main

import (
	"fmt"
	"os"
	"os/exec"
	"sync"

	"github.com/zyedidia/gotcl"
	"github.com/zyedidia/mak/expand"
)

type Executor struct {
	errfn     func(msg string)
	throttler chan struct{}
	m         *Machine
	mlock     sync.Mutex
}

func NewExecutor(max int, m *Machine, e func(msg string)) *Executor {
	return &Executor{
		m:         m,
		errfn:     e,
		throttler: make(chan struct{}, max),
	}
}

type command struct {
	name   string
	args   []string
	recipe string
}

func (e *Executor) ExecNode(n *node) {
	if !n.outOfDate() {
		return
	}

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

	if len(n.rule.Recipe) == 0 {
		return
	}

	e.mlock.Lock()
	e.m.itp.SetVarRaw("in", gotcl.FromList(n.rule.Prereqs))
	targets := make([]string, 0, len(n.rule.Targets))
	for _, t := range n.rule.Targets {
		targets = append(targets, t.str)
	}
	e.m.itp.SetVarRaw("out", gotcl.FromList(targets))
	commands := make([]command, 0, len(targets))
	for _, c := range n.rule.Recipe {
		rvar, rexpr := expandFuncs(e.m.itp)
		output, err := expand.ExpandSpecial(c, rvar, rexpr, '%')
		if err != nil {
			e.errfn(fmt.Sprintf("%v", err))
		}
		commands = append(commands, command{
			name:   "sh",
			args:   []string{"-c", output},
			recipe: output,
		})
	}
	e.mlock.Unlock()
	for _, c := range commands {
		e.ExecCommand(c)
	}
}

func (e *Executor) ExecCommand(c command) {
	fmt.Println(c.recipe)
	cmd := exec.Command(c.name, c.args...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	err := cmd.Run()
	if err != nil {
		e.errfn(fmt.Sprintf("%v", err))
	}
}
