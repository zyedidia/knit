package main

import (
	"fmt"
	"os"
	"os/exec"
	"sync"

	"github.com/zyedidia/gotcl"
	"github.com/zyedidia/mak/expand"
)

type executor struct {
	errfn     func(msg string)
	throttler chan struct{}
	vm        *tclvm
	mlock     sync.Mutex
}

func newExecutor(max int, vm *tclvm, e func(msg string)) *executor {
	return &executor{
		vm:        vm,
		errfn:     e,
		throttler: make(chan struct{}, max),
	}
}

type command struct {
	name   string
	args   []string
	recipe string
}

func (e *executor) execNode(n *node) {
	if !n.outOfDate() {
		return
	}

	var wg sync.WaitGroup
	for _, p := range n.prereqs {
		wg.Add(1)
		e.throttler <- struct{}{}
		go func(pn *node) {
			defer wg.Done()
			e.execNode(pn)
			<-e.throttler
		}(p)
	}
	wg.Wait()

	if len(n.rule.recipe) == 0 {
		return
	}

	e.mlock.Lock()
	e.vm.itp.SetVarRaw("in", gotcl.FromList(n.rule.prereqs))
	e.vm.itp.SetVarRaw("out", gotcl.FromList(n.rule.targets))
	if n.meta {
		e.vm.itp.SetVarRaw("stem", gotcl.FromStr(n.stem))
		e.vm.itp.SetVarRaw("matches", gotcl.FromList(n.matches))
	}
	commands := make([]command, 0, len(n.rule.recipe))
	for _, c := range n.rule.recipe {
		rvar, rexpr := expandFuncs(e.vm.itp)
		output, err := expand.Expand(c, rvar, rexpr)
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
		e.execCmd(c, n.rule.attrs.quiet)
	}
}

func (e *executor) execCmd(c command, quiet bool) {
	if !quiet {
		fmt.Println(c.recipe)
	}
	cmd := exec.Command(c.name, c.args...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	err := cmd.Run()
	if err != nil {
		e.errfn(fmt.Sprintf("%v", err))
	}
}
