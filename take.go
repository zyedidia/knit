package main

import (
	"fmt"
	"log"
	"os"
	"runtime"
	"strings"

	pflag "github.com/spf13/pflag"

	"github.com/zyedidia/gotcl"
)

type assign struct {
	name  string
	value string
}

var takefile = pflag.StringP("file", "f", "takefile", "takefile to use")
var ncpu = pflag.IntP("threads", "j", runtime.NumCPU(), "number of cores to use")
var viz = pflag.String("viz", "", "emit a graphiz file")
var dryrun = pflag.BoolP("dry-run", "n", false, "print commands without actually executing")
var rundir = pflag.StringP("directory", "C", "", "run command from directory")

func main() {
	pflag.Parse()

	if *rundir != "" {
		os.Chdir(*rundir)
	}

	args := pflag.Args()

	if *ncpu <= 0 {
		log.Fatal("you must enable at least 1 core!")
	}

	var vars []assign
	var targets []string
	for _, a := range args {
		before, after, found := strings.Cut(a, "=")
		if found {
			vars = append(vars, assign{
				name:  before,
				value: after,
			})
		} else {
			targets = append(targets, a)
		}
	}

	for _, e := range os.Environ() {
		env := strings.SplitN(e, "=", 2)
		vars = append(vars, assign{
			name:  env[0],
			value: env[1],
		})
	}

	data, err := os.ReadFile(*takefile)
	if err != nil {
		data, err = os.ReadFile(strings.Title(*takefile))
		if err != nil {
			log.Fatal(err)
		}
	}

	vm := newTclvm("rules")

	for _, v := range vars {
		vm.itp.SetVarRaw(v.name, gotcl.FromStr(v.value))
	}

	take, err := vm.Eval(string(data))
	if err != nil {
		log.Fatal(err)
	}

	rvar, rexpr := expandFuncs(vm.itp)

	rs := parse(take, *takefile, map[string][]string{}, errFns{
		printErr: func(e string) {
			fmt.Fprintln(os.Stderr, e)
		},
		errFn: func(e string) {
			fmt.Fprintln(os.Stderr, e)
			os.Exit(1)
		},
	}, expandFns{
		rvar:  rvar,
		rexpr: rexpr,
	})

	if len(targets) == 0 {
		if len(rs.directRules) == 0 {
			log.Fatal("no target given")
		}
		targets = rs.directRules[0].targets
	}

	rs.add(directRule{
		baseRule: baseRule{
			prereqs: targets,
			attrs: attrSet{
				virtual: true,
				noMeta:  true,
			},
		},
		targets: []string{":all"},
	})

	g, err := newGraph(rs, ":all")
	if err != nil {
		log.Fatalln(err)
	}
	if *viz != "" {
		f, err := os.Create(*viz)
		if err != nil {
			log.Fatal(err)
		}
		g.visualize(f)
		f.Close()
	}
	e := newExecutor(*ncpu, vm, func(msg string) {
		fmt.Fprintln(os.Stderr, msg)
	})
	e.execNode(g.base)
	err = e.saveDb()
	if err != nil {
		log.Fatal(err)
	}
}
