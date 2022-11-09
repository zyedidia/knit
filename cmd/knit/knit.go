package main

import (
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"runtime"
	"runtime/pprof"

	"github.com/spf13/pflag"
	"github.com/zyedidia/knit"
	"github.com/zyedidia/knit/info"
)

func fatal(a ...interface{}) {
	fmt.Fprintln(os.Stderr, a...)
	os.Exit(1)
}

func optString(flags *pflag.FlagSet, name, short string, val string, user *string, desc string) *string {
	if user != nil {
		return flags.StringP(name, short, *user, desc)
	}
	return flags.StringP(name, short, val, desc)
}

func optStringSlice(flags *pflag.FlagSet, name, short string, val []string, user *[]string, desc string) *[]string {
	if user != nil {
		return flags.StringSliceP(name, short, *user, desc)
	}
	return flags.StringSliceP(name, short, val, desc)
}

func optInt(flags *pflag.FlagSet, name, short string, val int, user *int, desc string) *int {
	if user != nil {
		return flags.IntP(name, short, *user, desc)
	}
	return flags.IntP(name, short, val, desc)
}

func optBool(flags *pflag.FlagSet, name, short string, val bool, user *bool, desc string) *bool {
	if user != nil {
		return flags.BoolP(name, short, *user, desc)
	}
	return flags.BoolP(name, short, val, desc)
}

func parseFlags(flags *pflag.FlagSet) ([]string, error) {
	var toolargs []string
	args := os.Args[1:]
	for i, a := range args {
		if a == "-t" || a == "--tool" {
			if i == len(args)-1 {
				return nil, fmt.Errorf("flag needs an argument: %s", a)
			}
			toolargs = args[i+2:]
			args = args[:i+2]
			break
		}
	}
	return toolargs, flags.Parse(args)
}

func main() {
	wd, err := os.Getwd()
	if err != nil {
		fatal(err)
	}

	user, err := knit.UserDefaults()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	main := pflag.NewFlagSet("main", pflag.ContinueOnError)

	knitfile := optString(main, "file", "f", "knitfile", user.Knitfile, "knitfile to use")
	ncpu := optInt(main, "threads", "j", runtime.NumCPU(), user.Ncpu, "number of cores to use")
	dryrun := optBool(main, "dry-run", "n", false, user.DryRun, "print commands without actually executing")
	rundir := optString(main, "directory", "C", "", user.RunDir, "run command from directory")
	always := optBool(main, "always-build", "B", false, user.Always, "unconditionally build all targets")
	quiet := optBool(main, "quiet", "q", false, user.Quiet, "don't print commands")
	style := optString(main, "style", "s", "basic", user.Style, "printer style to use (basic, steps, progress)")
	cache := optString(main, "cache", "", ".", user.CacheDir, "directory for caching internal build information")
	hash := optBool(main, "hash", "", true, user.Hash, "hash files to determine if they are out-of-date")
	updated := optStringSlice(main, "updated", "u", nil, user.Updated, "treat files as updated")
	root := optBool(main, "root", "r", false, user.Root, "run target relative to the root Knitfile")
	shell := optString(main, "shell", "", "sh", user.Shell, "shell to use when executing commands")
	keep := optBool(main, "keep-going", "", false, user.KeepGoing, "keep going even if recipes fail")

	debug := main.BoolP("debug", "D", false, "print debug information")
	tool := main.StringP("tool", "t", "", "subtool to invoke (use '-t list' to list subtools); further flags are passed to the subtool")
	version := main.BoolP("version", "v", false, "show version information")
	cpuprofile := main.String("cpuprofile", "", "write cpu profile to 'file'")
	help := main.BoolP("help", "h", false, "show this help message")

	toolargs, err := parseFlags(main)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	if *cpuprofile != "" {
		f, err := os.Create(*cpuprofile)
		if err != nil {
			fatal("could not create CPU profile: ", err)
		}
		defer f.Close()
		if err := pprof.StartCPUProfile(f); err != nil {
			fatal("could not start CPU profile: ", err)
		}
		defer pprof.StopCPUProfile()
	}

	if *help {
		fmt.Println("Usage of knit:")
		fmt.Println("  knit [TARGETS] [ARGS]")
		fmt.Println()
		fmt.Println("Options:")
		main.PrintDefaults()
		os.Exit(0)
	}

	if *version {
		fmt.Println("knit version", info.Version)
		os.Exit(0)
	}

	if *debug {
		log.SetOutput(os.Stdout)
		log.SetFlags(0)
		log.SetPrefix("[debug] ")
	} else {
		log.SetOutput(io.Discard)
	}

	out := os.Stdout
	file, err := knit.Run(out, main.Args(), knit.Flags{
		Knitfile:  *knitfile,
		Ncpu:      *ncpu,
		DryRun:    *dryrun,
		RunDir:    *rundir,
		Always:    *always,
		Quiet:     *quiet,
		Style:     *style,
		CacheDir:  *cache,
		Hash:      *hash,
		Updated:   *updated,
		Root:      *root,
		KeepGoing: *keep,
		Shell:     *shell,
		Tool:      *tool,
		ToolArgs:  toolargs,
	})

	rel, rerr := filepath.Rel(file, wd)
	if rerr != nil {
		rel = file
	}
	if file == "" {
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
