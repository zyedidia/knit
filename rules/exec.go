package rules

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"sync"

	"sync/atomic"

	"github.com/kballard/go-shellquote"
)

type Executor struct {
	errf      func(msg string)
	throttler chan struct{}
	w         io.Writer
	db        *Database
	lock      sync.Mutex
	stopped   atomic.Bool

	opts Options
}

type Options struct {
	NoExec       bool
	Shell        string
	AbortOnError bool
	BuildAll     bool
	Quiet        bool
}

func NewExecutor(db *Database, threads int, w io.Writer, opts Options, errf func(msg string)) *Executor {
	return &Executor{
		db:        db,
		errf:      errf,
		throttler: make(chan struct{}, threads-1),
		w:         w,
		opts:      opts,
	}
}

type command struct {
	name   string
	args   []string
	recipe string
}

var ErrNothingToDo = errors.New("nothing to be done")

func (e *Executor) Exec(g *Graph) error {
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt)
	if !e.execNode(g.base) {
		return ErrNothingToDo
	}
	return nil
}

func (e *Executor) execNode(n *node) bool {
	e.lock.Lock()
	if !e.opts.BuildAll && !n.outOfDate(e.db) {
		e.lock.Unlock()
		return false
	}
	e.db.InsertRecipe(n.rule.targets, n.recipe)
	e.lock.Unlock()

	var rebuilt atomic.Bool
	var wg sync.WaitGroup
	for _, p := range n.prereqs {
		wg.Add(1)
		select {
		case e.throttler <- struct{}{}:
			go func(pn *node) {
				defer wg.Done()
				r := e.execNode(pn)
				rebuilt.Store(rebuilt.Load() || r)
				<-e.throttler
			}(p)
		default:
			rebuilt.Store(e.execNode(p))
			wg.Done()
		}
	}
	wg.Wait()

	if len(n.rule.recipe) == 0 {
		return rebuilt.Load()
	}

	if e.stopped.Load() {
		return rebuilt.Load()
	}

	if n.rule.attrs.Exclusive {
		e.lock.Lock()
		defer e.lock.Unlock()
	}

	failed := false
	for _, cmd := range n.recipe {
		c, err := e.getCmd(cmd)
		if err != nil {
			e.errf(fmt.Sprintf("error while evaluating '%s': %v", cmd, err))
			failed = true
			break
		} else if c.recipe == "" {
			continue
		}
		if !n.rule.attrs.Quiet && !e.opts.Quiet {
			fmt.Fprintln(e.w, c.recipe)
		}
		if !e.opts.NoExec {
			err := e.execCmd(c)
			if err != nil {
				e.errf(fmt.Sprintf("error while executing '%s': %v", c.name, err))
				if e.opts.AbortOnError && !n.rule.attrs.NonStop {
					failed = true
					break
				}
			}
		}
	}

	if failed {
		for _, t := range n.rule.targets {
			err := os.RemoveAll(t)
			if err != nil {
				e.errf(fmt.Sprintf("error while removing failed targets: %v", err))
			}
		}
		e.stopped.Store(true)
	}
	return true
}

func (e *Executor) getCmd(cmd string) (command, error) {
	if e.opts.Shell != "" {
		return command{
			name:   e.opts.Shell,
			args:   []string{"-c", cmd},
			recipe: cmd,
		}, nil
	}
	words, err := shellquote.Split(cmd)
	if err != nil || len(words) == 0 {
		return command{}, err
	}
	return command{
		name:   words[0],
		args:   words[1:],
		recipe: cmd,
	}, nil
}

func (e *Executor) execCmd(c command) error {
	cmd := exec.Command(c.name, c.args...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}
