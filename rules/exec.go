package rules

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/kballard/go-shellquote"
)

type Printer interface {
	Print(cmd, dir string, name string, step int)
	SetSteps(nsteps int)
	Update()
	NeedsUpdate() bool
	Done(name string)
	Clear()
}

type Executor struct {
	printer Printer
	info    InfoFn

	db      *Database
	lock    sync.Mutex
	stopped atomic.Bool
	jobs    chan *node
	threads int

	steps int
	step  atomic.Int32

	rebuilt atomic.Bool
	err     error

	opts Options
}

type PrintCmdFn func(cmd string, dir string, step int, nsteps int)
type InfoFn func(msg string)

type Options struct {
	NoExec       bool   // don't execute recipes
	Shell        string // use shell for executing commands
	AbortOnError bool   // stop if an error happens in a recipe
	BuildAll     bool   // build all rules even if they are up-to-date
}

func NewExecutor(basedir string, db *Database, threads int, printer Printer, info InfoFn, opts Options) *Executor {
	return &Executor{
		db:      db,
		printer: printer,
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

	e.steps = g.Steps(true)
	e.printer.SetSteps(e.steps)

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

func (e *Executor) Clean(g *Graph) {
	e.steps = g.Steps(false)
	e.printer.SetSteps(e.steps)
	e.cleanNode(g.base)
}

func (e *Executor) cleanNode(n *node) {
	for _, p := range n.prereqs {
		e.cleanNode(p)
	}

	if n.done {
		return
	}

	// don't clean virtual rules or rules without a recipe to rebuild the outputs
	if len(n.rule.recipe) != 0 && !n.rule.attrs.Virtual {
		step := e.step.Add(1)
		for _, o := range n.outputs {
			if !o.exists && !e.opts.BuildAll {
				continue
			}
			if !e.opts.NoExec {
				err := o.remove()
				if err != nil {
					e.info(err.Error())
				}
			}
			e.printer.Print(fmt.Sprintf("remove %s", o.name), n.graph.dir, o.name, int(step))
			e.printer.Done(o.name)
		}
	}
	n.done = true
}

func (e *Executor) execNode(n *node) {
	e.lock.Lock()
	if !e.opts.BuildAll && !n.rule.attrs.Linked && !n.outOfDate(e.db) {
		e.lock.Unlock()
		n.setDone()
		return
	}
	e.lock.Unlock()

	e.db.Recipes.insert(n.rule.targets, n.recipe, n.graph.dir)

	for _, p := range n.prereqs {
		e.execNode(p)
		if len(p.recipe) == 0 {
			e.lock.Lock()
			for _, f := range p.outputs {
				e.db.Files.insert(f.name)
			}
			e.lock.Unlock()
		}
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

		step := e.step.Add(1)

		ruleName := strings.Join(n.rule.targets, " ")

		failed := false
		var execErr error
		for _, cmd := range n.recipe {
			c, err := e.getCmd(cmd, n.graph.dir)
			if err != nil {
				execErr = fmt.Errorf("'%s': error while evaluating '%s': %w", ruleName, cmd, err)
				failed = true
				break
			} else if c.recipe == "" {
				continue
			}
			if !n.rule.attrs.Quiet {
				e.printer.Print(c.recipe, c.dir, ruleName, int(step))
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
		e.printer.Done(ruleName)

		e.lock.Lock()

		for _, f := range n.outputs {
			e.db.Files.insert(f.name)
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
			e.err = execErr
		}

		e.lock.Unlock()
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

	if e.printer.NeedsUpdate() {
		stdout, _ := cmd.StdoutPipe()
		stderr, _ := cmd.StderrPipe()
		err := cmd.Start()
		if err != nil {
			return err
		}
		go forwardStream(e.printer, stdout, os.Stdout)
		go forwardStream(e.printer, stderr, os.Stderr)
		return cmd.Wait()
	}

	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func forwardStream(p Printer, stream io.Reader, w io.Writer) {
	buf := make([]byte, 1024)
	r := bufio.NewReader(stream)
	for n, err := r.Read(buf); err == nil; n, err = r.Read(buf) {
		p.Clear()
		w.Write(buf[:n])
		p.Update()
	}
}
