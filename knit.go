package main

import (
	"fmt"
	"os"
	"runtime"
	"strings"

	"github.com/spf13/pflag"
	"github.com/zyedidia/knit/info"
)

func fatalf(format string, args ...interface{}) {
	fmt.Fprintf(os.Stderr, format, args...)
	fmt.Fprintln(os.Stderr)
	os.Exit(1)
}

func fatal(s string) {
	fmt.Fprintln(os.Stderr, s)
	os.Exit(1)
}

func must(err error) {
	if err != nil {
		fatalf("%v", err)
	}
}

func assert(b bool) {
	if !b {
		panic("assertion failed")
	}
}

var flags = struct {
	knitfile *string
	ncpu     *int
	viz      *string
	dryrun   *bool
	rundir   *string
	always   *bool
	quiet    *bool
	version  *bool
}{
	knitfile: pflag.StringP("file", "f", "Knitfile", "Knitfile to use"),
	ncpu:     pflag.IntP("threads", "j", runtime.NumCPU(), "number of cores to use"),
	viz:      pflag.String("viz", "", "emit a graphiz file"),
	dryrun:   pflag.BoolP("dry-run", "n", false, "print commands without actually executing"),
	rundir:   pflag.StringP("directory", "C", "", "run command from directory"),
	always:   pflag.BoolP("always-build", "B", false, "unconditionally build all targets"),
	quiet:    pflag.BoolP("quiet", "q", false, "don't print commands"),
	version:  pflag.BoolP("version", "v", false, "show version information"),
}

type assign struct {
	name  string
	value string
}

func makeAssigns(args []string) ([]assign, []string) {
	assigns := make([]assign, 0, len(args))
	other := make([]string, 0)
	for _, a := range args {
		before, after, found := strings.Cut(a, "=")
		if found {
			assigns = append(assigns, assign{
				name:  before,
				value: after,
			})
		} else {
			other = append(other, a)
		}
	}
	return assigns, other
}

func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func main() {
	pflag.Parse()

	if *flags.version {
		fmt.Println("knit version", info.Version)
		os.Exit(0)
	}

	if *flags.rundir != "" {
		os.Chdir(*flags.rundir)
	}

	if *flags.ncpu <= 0 {
		fatal("you must enable at least 1 core")
	}

	if exists(strings.Title(*flags.knitfile)) {
		*flags.knitfile = strings.Title(*flags.knitfile)
	}

	args := pflag.Args()

	env := os.Environ()
	assigns, targets := makeAssigns(append(args, env...))

	f, err := os.Open(*flags.knitfile)
	must(err)

	vm := NewLuaVM()

	for _, v := range assigns {
		vm.SetVar(v.name, v.value)
	}

	_, err = vm.Eval(f, f.Name())
	must(err)

	f.Close()

	fmt.Println(targets)

	// rvar, rexpr := vm.ExpandFuncs()
	// for _, r := range vm.rules {
	// 	s, err := expand.Expand(r.Contents, rvar, rexpr)
	// 	if err != nil {
	// 		fatalf("%s:%d: in rule: %v", r.File, r.Line, err)
	// 	}
	// 	fmt.Println(s)
	// }
}
