package main

import (
	"fmt"
	"os"
	"os/exec"
	"sync"
)

type executor struct {
	errfn     func(msg string)
	throttler chan struct{}
	vm        *tclvm
	mlock     sync.Mutex
	db        *db
}

func newExecutor(max int, vm *tclvm, e func(msg string)) *executor {
	var d *db
	f, err := os.Open(".take")
	if err != nil {
		d = newDb()
	} else {
		d, err = newDbFromReader(f)
		if err != nil {
			d = newDb()
		}
		f.Close()
	}
	return &executor{
		vm:        vm,
		errfn:     e,
		throttler: make(chan struct{}, max),
		db:        d,
	}
}

type command struct {
	name   string
	args   []string
	recipe string
}

func (e *executor) execNode(n *node) {
	e.mlock.Lock()
	if !n.outOfDate(e.db, e.vm.itp) {
		e.mlock.Unlock()
		return
	}
	e.db.insert(n.rule.targets, n.recipe)
	e.mlock.Unlock()

	var wg sync.WaitGroup
	for _, p := range n.prereqs {
		wg.Add(1)
		select {
		case e.throttler <- struct{}{}:
			go func(pn *node) {
				defer wg.Done()
				e.execNode(pn)
				<-e.throttler
			}(p)
		default:
			e.execNode(p)
			wg.Done()
		}
	}
	wg.Wait()

	if len(n.rule.recipe) == 0 {
		return
	}

	commands := make([]command, 0, len(n.recipe))
	for _, cmd := range n.recipe {
		commands = append(commands, command{
			name:   "sh",
			args:   []string{"-c", cmd},
			recipe: cmd,
		})
	}

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

func (e *executor) saveDb() error {
	f, err := os.Create(".take")
	if err != nil {
		return err
	}
	defer f.Close()
	return e.db.ToWriter(f)
}
