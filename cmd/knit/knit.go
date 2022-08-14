package main

import (
	"errors"
	"fmt"
	"os"
	"runtime"

	"github.com/spf13/pflag"
	"github.com/zyedidia/knit"
	"github.com/zyedidia/knit/info"
	"github.com/zyedidia/knit/rules"
)

func main() {
	knitfile := pflag.StringP("file", "f", "knitfile", "knitfile to use")
	ncpu := pflag.IntP("threads", "j", runtime.NumCPU(), "number of cores to use")
	graph := pflag.String("graph", "", "emit the dependency graph to a file")
	dryrun := pflag.BoolP("dry-run", "n", false, "print commands without actually executing")
	rundir := pflag.StringP("directory", "C", "", "run command from directory")
	always := pflag.BoolP("always-build", "B", false, "unconditionally build all targets")
	quiet := pflag.BoolP("quiet", "q", false, "don't print commands")
	version := pflag.BoolP("version", "v", false, "show version information")
	help := pflag.BoolP("help", "h", false, "show this help message")

	pflag.Parse()

	if *help {
		pflag.Usage()
		os.Exit(0)
	}

	if *version {
		fmt.Println("knit version", info.Version)
		os.Exit(0)
	}

	out := os.Stdout

	err := knit.Run(out, pflag.Args(), knit.Flags{
		Knitfile: *knitfile,
		Ncpu:     *ncpu,
		Graph:    *graph,
		DryRun:   *dryrun,
		RunDir:   *rundir,
		Always:   *always,
		Quiet:    *quiet,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "knit: %s\n", err)
		if !errors.Is(err, rules.ErrNothingToDo) {
			os.Exit(1)
		}
	}
}
