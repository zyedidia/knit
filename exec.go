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

	// TODO: not thread safe
	e.m.itp.SetVarRaw("in", gotcl.FromList(n.rule.Prereqs))
	targets := make([]string, 0, len(n.rule.Targets))
	for _, t := range n.rule.Targets {
		targets = append(targets, t.str)
	}
	e.m.itp.SetVarRaw("out", gotcl.FromList(targets))
	for _, c := range n.rule.Recipe {
		e.ExecCommand(c)
	}
}

func (e *Executor) ExecCommand(c string) {
	e.mlock.Lock()
	rvar, rexpr := expandFuncs(e.m.itp)
	output, err := expand.ExpandSpecial(c, rvar, rexpr, '%')
	e.mlock.Unlock()
	if err != nil {
		e.errfn(fmt.Sprintf("%v", err))
	}
	fmt.Println(output)
	cmd := exec.Command("sh", "-c", output)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	err = cmd.Run()
	if err != nil {
		e.errfn(fmt.Sprintf("%v", err))
	}
}
