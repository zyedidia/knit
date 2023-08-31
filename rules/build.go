package rules

import (
	"bufio"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
)

type Printer interface {
	Print(cmd, dir string, name string, step int)
	SetSteps(nsteps int)
	Update()
	NeedsUpdate() bool
	Done(name string)
	Clear()
}

type InfoFn func(msg string)

type Options struct {
	NoExec       bool   // don't execute recipes
	Shell        string // use shell for executing commands
	AbortOnError bool   // stop if an error happens in a recipe
	BuildAll     bool   // build all rules even if they are up-to-date
	Hash         bool   // use hashes to determine whether a file has been modified
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
	e.steps = g.steps(e.db, e.opts.BuildAll, e.opts.Hash)
	e.printer.SetSteps(e.steps)

	// make sure ctrl-c doesn't kill this process, just the children
	if !e.opts.NoExec {
		sig := make(chan os.Signal, 1)
		signal.Notify(sig, syscall.SIGTERM, syscall.SIGINT, syscall.SIGQUIT, syscall.SIGHUP)
	}

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
	e.lock.Lock()
	if n.done {
		e.lock.Unlock()
		return
	}

	ood := n.outOfDate(e.db, e.opts.Hash, false)
	if !e.opts.BuildAll && !n.rule.attrs.Linked && ood == UpToDate {
		n.setDone(e.db, e.opts.NoExec, e.opts.Hash)
		e.lock.Unlock()
		return
	}
	e.lock.Unlock()
	// fmt.Println("exec", n.rule.targets)

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

		e.lock.Lock()
		// Cannot do dynamic step elision if hashing is disabled.
		ood := n.outOfDate(e.db, e.opts.Hash, e.opts.Hash)
		if !e.opts.BuildAll && !n.rule.attrs.Linked && (ood == UpToDate || ood == UpToDateDynamic) {
			done := n.setDone(e.db, e.opts.NoExec, e.opts.Hash)
			if !done && ood == UpToDateDynamic && len(n.rule.recipe) != 0 {
				log.Println(n.rule.targets, "elided")
				e.step.Add(1)
			}
			e.lock.Unlock()
			return
		}
		e.lock.Unlock()

		if ood == OnlyPrereqs {
			e.lock.Lock()
			n.setDone(e.db, e.opts.NoExec, e.opts.Hash)
			e.lock.Unlock()
			return
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
			e.lock.Lock()
			n.setDone(e.db, e.opts.NoExec, e.opts.Hash)
			e.lock.Unlock()
			continue
		}

		if e.stopped.Load() {
			e.lock.Lock()
			n.setDoneOrErr()
			e.lock.Unlock()
			continue
		}

		ruleName := strings.Join(n.rule.targets, " ")

		// make parent directories for outputs
		if !e.opts.NoExec {
			for _, o := range n.outputs {
				if len(n.recipe) != 0 {
					dir := filepath.Dir(o.name)
					if !exists(dir) {
						topdir := dir
						for !exists(filepath.Dir(topdir)) {
							topdir = filepath.Dir(topdir)
						}
						err := os.MkdirAll(dir, os.ModePerm)
						if err != nil {
							log.Println(err)
						}
						e.lock.Lock()
						e.db.AddOutputDir(topdir)
						e.lock.Unlock()
					}
				}
			}
		}

		// Lock is to ensure steps are printed in order
		e.lock.Lock()
		step := e.step.Add(1)

		failed := false
		var execErr error
		for i, cmd := range n.recipe {
			c, err := e.getCmd(cmd, n.dir)
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
			if i == 0 {
				e.lock.Unlock()
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
			n.setDoneOrErr()
		} else {
			n.setDone(e.db, e.opts.NoExec, e.opts.Hash)
		}

		e.rebuilt.Store(true)
		e.lock.Unlock()
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
	path, err := os.Executable()
	if err != nil {
		return command{}, err
	}
	return command{
		name:   path,
		args:   []string{"--shrun", cmd},
		recipe: cmd,
		dir:    dir,
	}, nil
}

func (e *Executor) execCmd(c command) error {
	// Save and reload DB when running a knit command from within knit
	if len(c.args) >= 2 && strings.HasPrefix(c.args[1], "knit ") {
		e.db.Save()
		defer e.db.Reload()
	}
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

// Returns true if 'path' exists.
func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
