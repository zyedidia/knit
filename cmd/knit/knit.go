package main

import (
	"errors"
	"fmt"
	"os"
	"runtime"

	"github.com/spf13/pflag"
	"github.com/zyedidia/knit"
	"github.com/zyedidia/knit/info"
)

func optString(name, short string, val string, user *string, desc string) *string {
	if user != nil {
		return pflag.StringP(name, short, *user, desc)
	}
	return pflag.StringP(name, short, val, desc)
}

func optInt(name, short string, val int, user *int, desc string) *int {
	if user != nil {
		return pflag.IntP(name, short, *user, desc)
	}
	return pflag.IntP(name, short, val, desc)
}

func optBool(name, short string, val bool, user *bool, desc string) *bool {
	if user != nil {
		return pflag.BoolP(name, short, *user, desc)
	}
	return pflag.BoolP(name, short, val, desc)
}

func main() {
	user, err := knit.UserDefaults()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	knitfile := optString("file", "f", "knitfile", user.Knitfile, "knitfile to use")
	ncpu := optInt("threads", "j", runtime.NumCPU(), user.Ncpu, "number of cores to use")
	graph := optString("graph", "", "", user.Graph, "emit the dependency graph to a file")
	dryrun := optBool("dry-run", "n", false, user.DryRun, "print commands without actually executing")
	rundir := optString("directory", "C", "", user.RunDir, "run command from directory")
	always := optBool("always-build", "B", false, user.Always, "unconditionally build all targets")
	quiet := optBool("quiet", "q", false, user.Quiet, "don't print commands")
	clean := optBool("clean", "c", false, user.Clean, "automatically clean files made by the given target")
	style := optString("style", "s", "basic", user.Style, "printer style to use (basic, steps, progress)")
	cache := optString("cache", "", ".", user.CacheDir, "directory for caching internal build information")
	hash := optBool("hash", "", true, user.Hash, "hash files to determine if they are out-of-date")
	commands := optBool("commands", "", false, user.Commands, "export compilation command database")

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
	err = knit.Run(out, pflag.Args(), knit.Flags{
		Knitfile: *knitfile,
		Ncpu:     *ncpu,
		Graph:    *graph,
		DryRun:   *dryrun,
		RunDir:   *rundir,
		Always:   *always,
		Quiet:    *quiet,
		Clean:    *clean,
		Style:    *style,
		CacheDir: *cache,
		Hash:     *hash,
		Commands: *commands,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "knit: %s\n", err)
		if !errors.Is(err, knit.ErrNothingToDo) {
			os.Exit(1)
		}
	}
}
