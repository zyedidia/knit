package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"

	"github.com/spf13/pflag"
	"github.com/zyedidia/knit"
)

func fatal(a ...interface{}) {
	fmt.Fprintln(os.Stderr, a...)
	os.Exit(1)
}

func main() {
	wd, err := os.Getwd()
	if err != nil {
		fatal(err)
	}

	file, err := knit.Run(os.Stdout, pflag.Args(), knit.Flags{
		Knitfile: "knitfile",
		Ncpu:     runtime.NumCPU(),
	})
	rel, rerr := filepath.Rel(wd, file)
	if rerr != nil {
		rel = file
	}
	if rel == "" {
		rel = "knit"
	}
	if errors.Is(err, knit.ErrQuiet) {
		return
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "%s: %s\n", rel, err)
		if !errors.Is(err, knit.ErrNothingToDo) {
			os.Exit(1)
		}
	}
}
