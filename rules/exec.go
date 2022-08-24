package rules

import (
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/kballard/go-shellquote"
)

type Executor struct {
	pRule PrintRuleFn
	pCmd  PrintCmdFn
	info  InfoFn

	db      *Database
	lock    sync.Mutex
	stopped atomic.Bool
	jobs    chan *node
	threads int

	rebuilt atomic.Bool
	err     error

	opts Options
}

type PrintRuleFn func(inputs, outputs, recipe []string, step, nsteps int)
type PrintCmdFn func(cmd string, dir string)
type InfoFn func(msg string)

type Options struct {
	NoExec       bool   // don't execute recipes
	Shell        string // use shell for executing commands
	AbortOnError bool   // stop if an error happens in a recipe
	BuildAll     bool   // Build all rules even if they are up-to-date
}

func NewExecutor(basedir string, db *Database, threads int, pRule PrintRuleFn, pCmd PrintCmdFn, info InfoFn, opts Options) *Executor {
	return &Executor{
		db:      db,
		pRule:   pRule,
		pCmd:    pCmd,
		opts:    opts,
		jobs:    make(chan *node, 128),
		threads: threads,
		info:    info,
	}
}

type command struct {
	name   string
	args   []string
	recipe string
	dir    string
}

// Exec runs all commands and returns true if something was rebuilt.
func (e *Executor) Exec(g *Graph) (bool, error) {
	// make sure ctrl-c doesn't kill this process, just the children
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt)

	for i := 0; i < e.threads; i++ {
		go e.runServer()
	}

	// send all jobs into e.jobs
	e.execNode(g.base)

	// wait for base to complete
	g.base.wait()

	// no more jobs to send
	close(e.jobs)

	return e.rebuilt.Load(), e.err
}

func (e *Executor) execNode(n *node) {
	if !e.opts.BuildAll && !n.rule.attrs.Linked && !n.outOfDate(e.db) {
		n.setDone()
		return
	}
	e.db.InsertRecipe(n.rule.targets, n.recipe, n.graph.dir)

	for _, p := range n.prereqs {
		e.execNode(p)
	}

	// We want to allow each job to be enqueued immediately when its prereqs
	// are done, so all nodes wait for their prereqs in parallel.
	dojob := func() {
		// wait for all prereqs to finish
		for _, p := range n.prereqs {
			p.wait()
		}

		n.cond.L.Lock()
		defer n.cond.L.Unlock()
		if n.queued {
			return
		}

		e.jobs <- n
		n.queued = true
	}

	// If we are not doing a parallel build there is no point in optimizing the
	// queueing order for parallelism, so there's no point to waiting in
	// parallel, and we'd rather not make goroutines so we can get
	// deterministic builds with ncpu=1.
	if e.threads == 1 {
		dojob()
	} else {
		go dojob()
	}
}

func (e *Executor) runServer() {
	for n := range e.jobs {
		if len(n.rule.recipe) == 0 {
			n.setDone()
			continue
		}

		if e.stopped.Load() {
			n.setDone()
			continue
		}

		locked := false
		if n.rule.attrs.Exclusive {
			e.lock.Lock()
			locked = true
		}

		failed := false
		if !n.rule.attrs.Quiet {
			e.pRule(n.rule.prereqs, n.rule.targets, n.recipe, 0, 0)
		}
		var execErr error
		for _, cmd := range n.recipe {
			c, err := e.getCmd(cmd, n.graph.dir)
			if err != nil {
				execErr = fmt.Errorf("'%s': error while evaluating '%s': %w", strings.Join(n.rule.targets, " "), cmd, err)
				failed = true
				break
			} else if c.recipe == "" {
				continue
			}
			if !n.rule.attrs.Quiet {
				e.pCmd(c.recipe, c.dir)
			}
			if !e.opts.NoExec {
				err := e.execCmd(c)
				if err != nil {
					execErr = fmt.Errorf("'%s': error during recipe: %w", strings.Join(n.rule.targets, " "), err)
					if e.opts.AbortOnError && !n.rule.attrs.NonStop {
						failed = true
						break
					}
				}
			}
		}

		if failed {
			if !n.rule.attrs.Virtual {
				for _, t := range n.rule.targets {
					e.info(fmt.Sprintf("removing '%s' due to failure", t))
					err := os.RemoveAll(t)
					if err != nil {
						execErr = fmt.Errorf("error while removing failed targets: %v", err)
					}
				}
			}
			e.stopped.Store(true)
			e.lock.Lock()
			e.err = execErr
			e.lock.Unlock()
		}

		if locked {
			e.lock.Unlock()
		}
		e.rebuilt.Store(true)
		n.setDone()
	}
}

func (e *Executor) getCmd(cmd string, dir string) (command, error) {
	if e.opts.Shell != "" {
		return command{
			name:   e.opts.Shell,
			args:   []string{"-c", cmd},
			recipe: cmd,
			dir:    dir,
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
		dir:    dir,
	}, nil
}

func (e *Executor) execCmd(c command) error {
	cmd := exec.Command(c.name, c.args...)
	cmd.Dir = c.dir
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}
