package rules

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"sync"

	"github.com/kballard/go-shellquote"
)

type Executor struct {
	errf      func(msg string)
	throttler chan struct{}
	w         io.Writer

	opts Options
}

type Options struct {
	NoExec       bool
	Shell        string
	AbortOnError bool
}

func NewExecutor(threads int, w io.Writer, opts Options, errf func(msg string)) *Executor {
	return &Executor{
		errf:      errf,
		throttler: make(chan struct{}, threads),
		w:         w,
		opts:      opts,
	}
}

type command struct {
	name   string
	args   []string
	recipe string
}

func (e *Executor) Exec(g *Graph) {
	e.execNode(g.base)
}

func (e *Executor) execNode(n *node) {
	if !n.outOfDate() {
		return
	}

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

	for _, cmd := range n.recipe {
		c, err := e.getCmd(cmd)
		if err != nil {
			e.errf(fmt.Sprintf("error while evaluating '%s': %v", cmd, err))
			if e.opts.AbortOnError {
				break
			} else {
				continue
			}
		} else if c.recipe == "" {
			continue
		}
		fmt.Fprintln(e.w, c.recipe)
		if !e.opts.NoExec {
			err := e.execCmd(c)
			if err != nil {
				e.errf(fmt.Sprintf("error while executing '%s': %v", c.name, err))
				if e.opts.AbortOnError {
					break
				}
			}
		}
	}
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
