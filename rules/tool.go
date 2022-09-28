package rules

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

var tools = []Tool{
	&ListTool{},
	&GraphTool{},
	&CleanTool{},
	&TargetsTool{},
	&CompileDbTool{},
	&CommandsTool{},
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
	t.dotNode(g.base, w, make(map[*info]bool))
	fmt.Fprintln(w, "}")
}

func (t *GraphTool) dotNode(n *node, w io.Writer, visited map[*info]bool) {
	if visited[n.info] {
		return
	}
	visited[n.info] = true
	for _, p := range n.prereqs {
		fmt.Fprintf(w, "    \"%s\" -> \"%s\";\n", t.str(n), t.str(p))
		t.dotNode(p, w, visited)
	}
}

func (t *GraphTool) text(g *Graph, w io.Writer) {
	t.textNode(g.base, w, make(map[*info]bool))
}

func (t *GraphTool) textNode(n *node, w io.Writer, visited map[*info]bool) {
	if visited[n.info] {
		return
	}
	visited[n.info] = true
	for _, p := range n.prereqs {
		fmt.Fprintf(w, "%s -> %s\n", t.str(n), t.str(p))
		t.textNode(p, w, visited)
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
		return fmt.Errorf("invalid argument '%s', must be one of: text, dot, pdf", choice)
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
		return fmt.Errorf("invalid argument '%s', must be one of: all, virtual", choice)
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
					Command:   strings.Join(n.recipe, "; "),
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

type CommandsTool struct {
	W io.Writer
}

type BuildRules []BuildCommand

func (r BuildRules) toMake(w io.Writer) {
	for _, c := range r {
		c.toMake(w)
	}
}

func (r BuildRules) toKnit(w io.Writer) {
	fmt.Fprintln(w, "return r{")
	for _, c := range r {
		c.toKnit(w)
	}
	fmt.Fprintln(w, "}")
}

func (r BuildRules) toNinja(w io.Writer) {
	for _, c := range r {
		c.toNinja(w)
	}
}

type BuildCommand struct {
	Directory string   `json:"directory"`
	Prereqs   []string `json:"prereqs"`
	Inputs    []string `json:"inputs"`
	Outputs   []string `json:"outputs"`
	Commands  []string `json:"command"`
	Name      string   `json:"name"`
}

func (c *BuildCommand) toMake(w io.Writer) {
	buf := &bytes.Buffer{}

	if len(c.Outputs) == 0 {
		buf.WriteString(c.Name)
	} else {
		buf.WriteString(strings.Join(c.Outputs, " "))
		if len(c.Outputs) > 1 {
			buf.WriteString(" &")
		}
	}
	buf.WriteString(": ")
	buf.WriteString(strings.Join(c.Prereqs, " "))
	buf.WriteByte('\n')

	cd := ""
	if c.Directory != "." && c.Directory != "" {
		cd = "cd " + c.Directory + "; "
	}
	for _, cmd := range c.Commands {
		buf.WriteByte('\t')
		buf.WriteString(cd + cmd)
		buf.WriteByte('\n')
	}
	w.Write(buf.Bytes())
}

func (c *BuildCommand) toKnit(w io.Writer) {
	buf := &bytes.Buffer{}

	buf.WriteString("$ ")

	if len(c.Outputs) == 0 {
		buf.WriteString(c.Name)
	} else {
		buf.WriteString(strings.Join(c.Outputs, " "))
	}
	buf.WriteString(": ")
	buf.WriteString(strings.Join(c.Prereqs, " "))
	buf.WriteByte('\n')

	cd := ""
	if c.Directory != "." && c.Directory != "" {
		cd = "cd " + c.Directory + "; "
	}
	for _, cmd := range c.Commands {
		buf.WriteByte('\t')
		buf.WriteString(cd + cmd)
		buf.WriteByte('\n')
	}
	w.Write(buf.Bytes())
}

func (c *BuildCommand) toNinja(w io.Writer) {
	if len(c.Commands) > 0 {
		fmt.Fprintf(w, "rule %s\n", strings.Replace(c.Name, "/", "_", -1))
		cd := ""
		if c.Directory != "." && c.Directory != "" {
			cd = "cd " + c.Directory + "; "
		}
		fmt.Fprintf(w, "  command = %s%s\n", cd, strings.Join(c.Commands, "; "))
	}
	out := c.Name
	if len(c.Outputs) > 1 {
		out = strings.Join(c.Outputs, " ")
	}
	rule := strings.Replace(c.Name, "/", "_", -1)
	if len(c.Commands) == 0 {
		rule = "phony"
	}
	fmt.Fprintf(w, "build %s: %s %s\n", out, rule, strings.Join(c.Prereqs, " "))
}

func (t *CommandsTool) commands(n *node, visited map[*info]bool, cmds BuildRules) BuildRules {
	if visited[n.info] || len(n.rule.prereqs) == 0 && len(n.rule.recipe) == 0 {
		return cmds
	}

	inputs := n.prereqsSub(false)
	for i, p := range inputs {
		inputs[i] = filepath.Join(n.graph.dir, p)
	}
	prs := n.prereqsSub(true)
	for i, p := range prs {
		prs[i] = filepath.Join(n.graph.dir, p)
	}
	outputs := []string{}
	for _, o := range n.outputs {
		outputs = append(outputs, filepath.Clean(o.name))
	}
	cmds = append(cmds, BuildCommand{
		Directory: n.graph.dir,
		Prereqs:   prs,
		Inputs:    inputs,
		Outputs:   outputs,
		Name:      filepath.Join(n.graph.dir, n.myTarget),
		Commands:  n.recipe,
	})

	visited[n.info] = true

	for _, p := range n.prereqs {
		cmds = t.commands(p, visited, cmds)
	}
	return cmds
}

func (t *CommandsTool) shell(n *node, visited map[*info]bool, w io.Writer) {
	if visited[n.info] {
		return
	}
	visited[n.info] = true

	for _, p := range n.prereqs {
		t.shell(p, visited, w)
	}
	cd := ""
	if n.graph.dir != "." && n.graph.dir != "" {
		cd = "cd " + n.graph.dir + ";"
	}
	for _, c := range n.recipe {
		var cmd string
		if cd != "" {
			cmd = fmt.Sprintf("(%s %s)\n", cd, c)
		} else {
			cmd = c + "\n"
		}
		w.Write([]byte(cmd))
	}
}

func (t *CommandsTool) Run(g *Graph, args []string) error {
	choice := "knit"
	if len(args) > 0 {
		choice = args[0]
	}

	cmds := t.commands(g.base, make(map[*info]bool), []BuildCommand{})

	switch choice {
	case "knit":
		cmds.toKnit(t.W)
	case "make":
		cmds.toMake(t.W)
	case "ninja":
		cmds.toNinja(t.W)
	case "json":
		data, err := json.Marshal(cmds)
		if err != nil {
			return err
		}
		t.W.Write(data)
		_, err = t.W.Write([]byte{'\n'})
		return err
	case "shell":
		t.shell(g.base, make(map[*info]bool), t.W)
	default:
		return fmt.Errorf("invalid argument '%s', must be one of: knit, json, make, ninja, shell", choice)
	}
	return nil
}

func (t *CommandsTool) String() string {
	return "commands - output the build commands (formats: knit, json, make, ninja, shell)"
}

// TODO: status tool
// type StatusTool struct{}
//
// func (t *StatusTool) visit(n *node, visited map[*info]bool) {
// 	if visited[n.info] {
// 		return
// 	}
//
// 	visited[n.info] = true
// 	for _, p := range n.prereqs {
// 		t.visit(p, visited)
// 	}
// }
//
// func (t *StatusTool) Run(g *Graph, args []string) error {
// 	return nil
// }
//
// func (t *StatusTool) String() string {
// 	return "status - output dependency status information"
// }
