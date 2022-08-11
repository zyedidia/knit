package main

import (
	"fmt"
	"io"
	"os"
	"runtime"

	"github.com/spf13/pflag"
	"github.com/zyedidia/knit"
	"github.com/zyedidia/knit/info"
)

func main() {
	knitfile := pflag.StringP("file", "f", "knitfile", "knitfile to use")
	ncpu := pflag.IntP("threads", "j", runtime.NumCPU(), "number of cores to use")
	viz := pflag.String("viz", "", "emit a graphiz file")
	dryrun := pflag.BoolP("dry-run", "n", false, "print commands without actually executing")
	rundir := pflag.StringP("directory", "C", "", "run command from directory")
	always := pflag.BoolP("always-build", "B", false, "unconditionally build all targets")
	quiet := pflag.BoolP("quiet", "q", false, "don't print commands")
	version := pflag.BoolP("version", "v", false, "show version information")
	help := pflag.BoolP("help", "h", false, "")

	pflag.Parse()

	if *help {
		pflag.Usage()
		os.Exit(0)
	}

	if *version {
		fmt.Println("knit version", info.Version)
		os.Exit(0)
	}

	var out io.Writer
	if *quiet {
		out = io.Discard
	} else {
		out = os.Stdout
	}

	knit.Run(out, pflag.Args(), knit.Flags{
		Knitfile: *knitfile,
		Ncpu:     *ncpu,
		Viz:      *viz,
		DryRun:   *dryrun,
		RunDir:   *rundir,
		Always:   *always,
	})
}
