package knit

import (
	"fmt"
	"io"
	"path/filepath"
	"sync"

	pb "github.com/schollz/progressbar/v3"
)

type BasicPrinter struct {
	w    io.Writer
	lock sync.Mutex
}

func (p *BasicPrinter) SetSteps(int)      {}
func (p *BasicPrinter) Update()           {}
func (p *BasicPrinter) Clear()            {}
func (p *BasicPrinter) Done(string)       {}
func (p *BasicPrinter) NeedsUpdate() bool { return false }

func (p *BasicPrinter) Print(cmd, dir string, name string, step int) {
	p.lock.Lock()
	defer p.lock.Unlock()
	if dir != "." {
		fmt.Fprintf(p.w, "[%s] ", dir)
	}
	fmt.Fprintln(p.w, cmd)
}

type StepPrinter struct {
	w     io.Writer
	lock  sync.Mutex
	steps int
}

func (p *StepPrinter) SetSteps(steps int) {
	p.steps = steps
}
func (p *StepPrinter) Update()           {}
func (p *StepPrinter) Clear()            {}
func (p *StepPrinter) Done(string)       {}
func (p *StepPrinter) NeedsUpdate() bool { return false }

func (p *StepPrinter) Print(cmd, dir string, name string, step int) {
	p.lock.Lock()
	defer p.lock.Unlock()
	fmt.Fprintf(p.w, "[%d/%d] ", step, p.steps)
	if dir != "." {
		fmt.Fprintf(p.w, "[%s] ", dir)
	}
	fmt.Fprintln(p.w, cmd)
}

type ProgressPrinter struct {
	w            io.Writer
	lock         sync.Mutex
	bar          *pb.ProgressBar
	tasks        map[string]string
	showCommands bool
}

func (p *ProgressPrinter) SetSteps(steps int) {
	p.bar = pb.NewOptions64(int64(steps),
		pb.OptionSetWriter(p.w),
		pb.OptionSetWidth(10),
		pb.OptionShowCount(),
		pb.OptionSpinnerType(14),
		pb.OptionFullWidth(),
		pb.OptionSetPredictTime(false),
		pb.OptionSetDescription("Building"),
		pb.OptionOnCompletion(func() {
			fmt.Fprint(p.w, "\n")
		}),
		pb.OptionSetTheme(pb.Theme{
			Saucer:        "=",
			SaucerHead:    ">",
			SaucerPadding: " ",
			BarStart:      "[",
			BarEnd:        "]",
		}))
}

func (p *ProgressPrinter) NeedsUpdate() bool { return true }

func (p *ProgressPrinter) Update() {
	p.lock.Lock()
	defer p.lock.Unlock()
	p.bar.RenderBlank()
	fmt.Fprint(p.w, "\r")
}

func (p *ProgressPrinter) Clear() {
	p.lock.Lock()
	defer p.lock.Unlock()
	p.bar.Clear()
	fmt.Fprint(p.w, "\r")
}

func (p *ProgressPrinter) Print(cmd, dir string, name string, step int) {
	p.lock.Lock()
	defer p.lock.Unlock()
	p.tasks[name] = cmd

	if p.showCommands {
		p.bar.Clear()
		fmt.Fprint(p.w, "\r")
		if dir != "." {
			fmt.Fprintf(p.w, "[%s] ", dir)
		}
		fmt.Fprintln(p.w, cmd)
	}

	p.bar.Describe(p.desc())
	p.bar.RenderBlank()
}

func (p *ProgressPrinter) desc() string {
	desc := "Building"
	for k := range p.tasks {
		desc += " " + fmt.Sprintf("%-40s", filepath.Base(k))
		// before, _, found := strings.Cut(cmd, " ")
		// if found {
		// 	desc += " [" + before + "]"
		// }
		break
	}
	return desc
}

func (p *ProgressPrinter) Done(name string) {
	p.lock.Lock()
	defer p.lock.Unlock()
	delete(p.tasks, name)
	if len(p.tasks) == 0 {
		p.bar.Describe("Built " + name)
	} else {
		p.bar.Describe(p.desc())
	}
	p.bar.Add(1)
}
