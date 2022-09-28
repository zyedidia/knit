package rules

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
)

var tools = []Tool{
	&ListTool{},
	&GraphTool{},
	&CleanTool{},
	&RulesTool{},
	&TargetsTool{},
	&CompileDbTool{},
	&BuildDbTool{},
}

type Tool interface {
	Run(g *Graph, args []string) error
	String() string
}

type ListTool struct {
	W io.Writer
}

func (t *ListTool) Run(g *Graph, args []string) error {
	for _, tl := range tools {
		fmt.Fprintln(t.W, tl)
	}

	return nil
}

func (t *ListTool) String() string {
	return "list - list all available tools"
}

type GraphTool struct {
	W io.Writer
}

func (t *GraphTool) str(n *node) string {
	if n.graph.dir == "" || n.graph.dir == "." {
		return n.myTarget
	}
	return fmt.Sprintf("[%s]%s", n.graph.dir, n.myTarget)
}

func (t *GraphTool) dot(g *Graph, w io.Writer) {
	fmt.Fprintln(w, "digraph take {")
	t.dotNode(g.base, w)
	fmt.Fprintln(w, "}")
}

func (t *GraphTool) dotNode(n *node, w io.Writer) {
	for _, p := range n.prereqs {
		fmt.Fprintf(w, "    \"%s\" -> \"%s\";\n", t.str(n), t.str(p))
		t.dotNode(p, w)
	}
}

func (t *GraphTool) text(g *Graph, w io.Writer) {
	t.textNode(g.base, w)
}

func (t *GraphTool) textNode(n *node, w io.Writer) {
	for _, p := range n.prereqs {
		fmt.Fprintf(w, "%s -> %s\n", t.str(n), t.str(p))
		t.textNode(p, w)
	}
}

func (t *GraphTool) Run(g *Graph, args []string) error {
	choice := "text"
	if len(args) > 0 {
		choice = args[0]
	}
	switch choice {
	case "text":
		t.text(g, t.W)
	case "dot":
		t.dot(g, t.W)
	case "pdf":
		in := &bytes.Buffer{}
		t.dot(g, in)
		cmd := exec.Command("dot", "-Tpdf")
		cmd.Stdout = t.W
		cmd.Stdin = in
		cmd.Stderr = os.Stderr
		err := cmd.Run()
		if err != nil {
			return err
		}
	default:
		return fmt.Errorf("invalid argument '%s', must be one of 'text', 'dot', 'pdf'", choice)
	}
	return nil
}

func (t *GraphTool) String() string {
	return "graph - print build graph in specified format: text, dot, pdf"
}

type CleanTool struct {
	NoExec bool
	All    bool
	W      io.Writer

	err error
}

func (t *CleanTool) clean(n *node, done map[*info]bool) {
	for _, p := range n.prereqs {
		t.clean(p, done)
	}

	if done[n.info] {
		return
	}

	// don't clean virtual rules or rules without a recipe to rebuild the outputs
	if len(n.rule.recipe) != 0 && !n.rule.attrs.Virtual {
		for _, o := range n.outputs {
			if !o.exists && !t.All {
				continue
			}
			if !t.NoExec {
				err := o.remove()
				if err != nil {
					t.err = err
					continue
				}
			}
			if n.graph.dir != "." && n.graph.dir != "" {
				fmt.Fprintf(t.W, "[%s] ", n.graph.dir)
			}
			fmt.Fprintln(t.W, "remove", o.name)
		}
	}
	done[n.info] = true
}

func (t *CleanTool) Run(g *Graph, args []string) error {
	t.clean(g.base, make(map[*info]bool))
	return t.err
}

func (t *CleanTool) String() string {
	return "clean - remove all files produced by the build"
}

type RulesTool struct {
	W io.Writer
}

func (t *RulesTool) visit(n *node, visited map[*info]bool) {
	if visited[n.info] || len(n.rule.recipe) == 0 && len(n.rule.prereqs) == 0 {
		return
	}

	fmt.Fprintf(t.W, "%s: %s\n\t%s\n", strings.Join(n.rule.targets, " "), strings.Join(n.rule.prereqs, " "), strings.Join(n.recipe, "\n\t"))

	visited[n.info] = true
	for _, p := range n.prereqs {
		t.visit(p, visited)
	}
}

func (t *RulesTool) Run(g *Graph, args []string) error {
	t.visit(g.base, make(map[*info]bool))
	return nil
}

func (t *RulesTool) String() string {
	return "rules - print rules for the build"
}

type TargetsTool struct {
	W io.Writer
}

func (t *TargetsTool) targets(n *node, virtual bool, visited map[*info]bool) {
	if visited[n.info] || len(n.rule.recipe) == 0 && len(n.rule.prereqs) == 0 {
		return
	}

	if !virtual || virtual && n.rule.attrs.Virtual {
		fmt.Fprintln(t.W, strings.Join(n.rule.targets, "\n"))
	}

	visited[n.info] = true
	for _, p := range n.prereqs {
		t.targets(p, virtual, visited)
	}
}

func (t *TargetsTool) Run(g *Graph, args []string) error {
	choice := "all"
	if len(args) > 0 {
		choice = args[0]
	}
	visited := make(map[*info]bool)
	switch choice {
	case "all":
		t.targets(g.base, false, visited)
	case "virtual":
		t.targets(g.base, true, visited)
	default:
		return fmt.Errorf("invalid argument '%s', must be one of 'all', 'virtual'", choice)
	}
	return nil
}

func (t *TargetsTool) String() string {
	return "targets - list all targets (pass 'virtual' for just virtual targets)"
}

type CompileDbTool struct {
	W io.Writer
}

type CompCommand struct {
	Directory string `json:"directory"`
	File      string `json:"file"`
	Command   string `json:"command"`
}

func (t *CompileDbTool) visit(n *node, visited map[*info]bool, cmds []CompCommand) []CompCommand {
	if visited[n.info] {
		return cmds
	}

	visited[n.info] = true

	for _, p := range n.prereqs {
		if len(p.prereqs) == 0 {
			for _, o := range p.outputs {
				cmds = append(cmds, CompCommand{
					Directory: p.graph.dir,
					File:      o.name,
					Command:   strings.Join(n.recipe, ";"),
				})
			}
		} else {
			cmds = t.visit(p, visited, cmds)
		}
	}
	return cmds
}

func (t *CompileDbTool) Run(g *Graph, args []string) error {
	cmds := t.visit(g.base, make(map[*info]bool), []CompCommand{})
	data, err := json.Marshal(cmds)
	if err != nil {
		return err
	}
	t.W.Write(data)
	_, err = t.W.Write([]byte{'\n'})
	return err
}

func (t *CompileDbTool) String() string {
	return "compdb - output a compile commands database"
}

type BuildDbTool struct {
	W io.Writer
}

type BuildCommand struct {
	Directory string   `json:"directory"`
	Inputs    []string `json:"inputs"`
	Outputs   []string `json:"outputs"`
	Command   string   `json:"command"`
	Name      string   `json:"name"`
}

func (t *BuildDbTool) visit(n *node, visited map[*node]bool, cmds []BuildCommand) []BuildCommand {
	if visited[n] || len(n.rule.prereqs) == 0 && len(n.rule.recipe) == 0 {
		return cmds
	}

	prs := n.prereqsSub()
	outputs := []string{}
	for _, o := range n.outputs {
		outputs = append(outputs, o.name)
	}
	cmds = append(cmds, BuildCommand{
		Directory: n.graph.dir,
		Inputs:    prs,
		Outputs:   outputs,
		Name:      n.myTarget,
		Command:   strings.Join(n.recipe, ";"),
	})

	visited[n] = true

	for _, p := range n.prereqs {
		cmds = t.visit(p, visited, cmds)
	}
	return cmds
}

func (t *BuildDbTool) Run(g *Graph, args []string) error {
	cmds := t.visit(g.base, make(map[*node]bool), []BuildCommand{})
	data, err := json.Marshal(cmds)
	if err != nil {
		return err
	}
	t.W.Write(data)
	_, err = t.W.Write([]byte{'\n'})
	return err
}

func (t *BuildDbTool) String() string {
	return "builddb - output a build information database"
}

// TODO: status tool
type StatusTool struct{}

func (t *StatusTool) visit(n *node, visited map[*info]bool) {
	if visited[n.info] {
		return
	}

	visited[n.info] = true
	for _, p := range n.prereqs {
		t.visit(p, visited)
	}
}

func (t *StatusTool) Run(g *Graph, args []string) error {
	return nil
}

func (t *StatusTool) String() string {
	return "status - output dependency status information"
}
