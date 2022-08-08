package main

import (
	"os"
	"runtime"

	"github.com/spf13/pflag"
	"github.com/zyedidia/knit"
)

func main() {
	pflag.Parse()

	knit.Run(os.Stdout, pflag.Args(), knit.Flags{
		Knitfile: "knitfile",
		Ncpu:     runtime.NumCPU(),
	})
}
