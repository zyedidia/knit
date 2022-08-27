package knit

import (
	"fmt"
	"io"
	"strings"
	"sync"
	"time"

	pb "github.com/schollz/progressbar/v3"
)

type BasicPrinter struct {
	w    io.Writer
	lock sync.Mutex
}

func (p *BasicPrinter) SetSteps(int) {}
func (p *BasicPrinter) Update()      {}

func (p *BasicPrinter) Print(cmd, dir string, name string, step int) {
	p.lock.Lock()
	defer p.lock.Unlock()
	if dir != "." {
		fmt.Fprintf(p.w, "[in %s] ", dir)
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
func (p *StepPrinter) Update() {}

func (p *StepPrinter) Print(cmd, dir string, name string, step int) {
	p.lock.Lock()
	defer p.lock.Unlock()
	fmt.Fprintf(p.w, "[%d/%d] ", step, p.steps)
	if dir != "." {
		fmt.Fprintf(p.w, "[in %s] ", dir)
	}
	fmt.Fprintln(p.w, cmd)
}

type ProgressPrinter struct {
	w     io.Writer
	lock  sync.Mutex
	bar   *pb.ProgressBar
	tasks map[string]string
}

func (p *ProgressPrinter) SetSteps(steps int) {
	p.bar = pb.NewOptions64(int64(steps),
		pb.OptionSetWriter(p.w),
		pb.OptionSetWidth(10),
		pb.OptionThrottle(65*time.Millisecond),
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

func (p *ProgressPrinter) Update() {
	fmt.Print(p.bar.String())
	fmt.Print("\r")
}

func (p *ProgressPrinter) Clear() {
	p.bar.Clear()
	fmt.Print("\r")
}

func (p *ProgressPrinter) Print(cmd, dir string, name string, step int) {
	p.lock.Lock()
	defer p.lock.Unlock()
	p.tasks[name] = cmd
	p.describe()
	p.Update()
}

func (p *ProgressPrinter) describe() {
	desc := "Building"
	for k, cmd := range p.tasks {
		desc += " " + k
		before, _, found := strings.Cut(cmd, " ")
		if found {
			desc += " [" + before + "]"
		}
		break
	}
	p.bar.Describe(desc)
}

func (p *ProgressPrinter) Done(name string) {
	p.lock.Lock()
	delete(p.tasks, name)
	p.describe()
	p.bar.Add(1)
	p.lock.Unlock()
}
