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

func n2str(n *node) string {
	if n.dir == "" || n.dir == "." {
		return n.myTarget
	}
	return filepath.Join(n.dir, n.myTarget)
}

var tools = []Tool{
	&ListTool{},
	&GraphTool{},
	&CleanTool{},
	&TargetsTool{},
	&CompileDbTool{},
	&CommandsTool{},
	&StatusTool{},
	&PathTool{},
	&DbTool{},
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

func (t *GraphTool) dot(g *Graph, w io.Writer) {
	fmt.Fprintln(w, "strict digraph take {")
	fmt.Fprintln(w, "rankdir=\"LR\";")
	t.dotNode(g.base, w, make(map[*node]bool))
	fmt.Fprintln(w, "}")
}

func (t *GraphTool) dotNode(n *node, w io.Writer, visited map[*node]bool) {
	if visited[n] {
		return
	}
	visited[n] = true
	for _, p := range n.prereqs {
		fmt.Fprintf(w, "    \"%s\" -> \"%s\";\n", n2str(p), n2str(n))
		t.dotNode(p, w, visited)
	}
}

func (t *GraphTool) text(n *node, w io.Writer, visited map[*node]bool) {
	if visited[n] {
		return
	}
	visited[n] = true
	for _, p := range n.prereqs {
		fmt.Fprintf(w, "%s -> %s\n", n2str(n), n2str(p))
		t.text(p, w, visited)
	}
}

func (t *GraphTool) tree(indent string, n *node, w io.Writer) {
	fmt.Fprintf(w, "%s%s\n", indent, n2str(n))
	for _, p := range n.prereqs {
		t.tree(indent+"| ", p, w)
	}
}

func (t *GraphTool) Run(g *Graph, args []string) error {
	choice := "text"
	if len(args) > 0 {
		choice = args[0]
	}
	switch choice {
	case "text":
		t.text(g.base, t.W, make(map[*node]bool))
	case "tree":
		t.tree("", g.base, t.W)
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
		return fmt.Errorf("invalid argument '%s', must be one of: text, tree, dot, pdf", choice)
	}
	return nil
}

func (t *GraphTool) String() string {
	return "graph - print build graph in specified format: text, tree, dot, pdf"
}

type CleanTool struct {
	NoExec bool
	Db     *Database
	W      io.Writer
}

// removes empty dirs and dirs containing only empty dirs
func (t *CleanTool) removeEmpty(dir string) error {
	ents, err := os.ReadDir(dir)
	if err != nil {
		delete(t.Db.OutputDirs, dir)
		return err
	}

	for _, ent := range ents {
		if !ent.IsDir() {
			// cannot remove
			return nil
		}
		err := t.removeEmpty(filepath.Join(dir, ent.Name()))
		if err != nil {
			return err
		}
	}

	ents, err = os.ReadDir(dir)
	if err != nil {
		return err
	}

	if len(ents) == 0 {
		if !t.NoExec {
			err := os.Remove(dir)
			delete(t.Db.OutputDirs, dir)
			if err != nil {
				return err
			}
		}
		fmt.Fprintln(t.W, "remove", dir)
		return nil
	}

	return nil
}

func (t *CleanTool) Run(g *Graph, args []string) error {
	for o := range t.Db.Outputs {
		if !t.NoExec {
			err := os.RemoveAll(o)
			delete(t.Db.Outputs, o)
			if err != nil {
				fmt.Fprintln(os.Stderr, err)
				continue
			}
		}
		fmt.Fprintln(t.W, "remove", o)
	}
	for o := range t.Db.OutputDirs {
		err := t.removeEmpty(o)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			continue
		}
		if t.NoExec {
			fmt.Fprintln(t.W, "remove if empty", o)
		}
	}
	return nil
}

func (t *CleanTool) String() string {
	return "clean - remove all files produced by the build"
}

type TargetsTool struct {
	W io.Writer
}

func (t *TargetsTool) targets(n *node, virtual bool, outputs bool, visited map[*info]bool) {
	if visited[n.info] || len(n.rule.recipe) == 0 && len(n.rule.prereqs) == 0 {
		return
	}

	if virtual && n.rule.attrs.Virtual || outputs && !n.rule.attrs.Virtual {
		for _, targ := range n.rule.targets {
			fmt.Fprintln(t.W, pathJoin(n.dir, targ))
		}
	}

	visited[n.info] = true
	for _, p := range n.prereqs {
		t.targets(p, virtual, outputs, visited)
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
		t.targets(g.base, true, true, visited)
	case "virtual":
		t.targets(g.base, true, false, visited)
	case "outputs":
		t.targets(g.base, false, true, visited)
	default:
		return fmt.Errorf("invalid argument '%s', must be one of: all, virtual", choice)
	}
	return nil
}

func (t *TargetsTool) String() string {
	return "targets - list all targets (pass 'virtual' for just virtual targets, pass 'outputs' for just output targets)"
}

type CompileDbTool struct {
	W   io.Writer
	All bool
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

	prereqs := n.myExpPrereqs
	if t.All {
		prereqs = n.myPrereqs
	}
	for _, p := range prereqs {
		dir, err := filepath.Abs(n.dir)
		if err != nil {
			continue
		}
		cmds = append(cmds, CompCommand{
			Directory: dir,
			File:      p,
			Command:   strings.Join(n.recipe, "; "),
		})
	}
	for _, p := range n.prereqs {
		cmds = t.visit(p, visited, cmds)
	}
	return cmds
}

func (t *CompileDbTool) Run(g *Graph, args []string) error {
	if len(args) > 0 && args[0] == "all" {
		t.All = true
	}

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

	// don't write special rules
	if !strings.HasPrefix(n.myTarget, ":") {
		inputs := n.inputs()
		for i, p := range inputs {
			inputs[i] = filepath.Join(n.dir, p)
		}
		prs := n.myPrereqs
		for i, p := range prs {
			prs[i] = filepath.Join(n.dir, p)
		}
		outputs := []string{}
		for _, o := range n.outputs {
			outputs = append(outputs, filepath.Clean(o.name))
		}

		cmds = append(cmds, BuildCommand{
			Directory: n.dir,
			Prereqs:   prs,
			Inputs:    inputs,
			Outputs:   outputs,
			Name:      filepath.Join(n.dir, n.myTarget),
			Commands:  n.recipe,
		})
	}

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
	if n.dir != "." && n.dir != "" {
		cd = "cd " + n.dir + ";"
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

type StatusTool struct {
	W    io.Writer
	Db   *Database
	Hash bool
}

func (t *StatusTool) visit(prev UpdateReason, indent string, n *node, visited map[*node]bool) {
	status := n.outOfDate(t.Db, t.Hash, false)
	if n.rule.attrs.Linked && status == UpToDate && prev != UpToDate {
		status = LinkedUpdate
	}
	fmt.Fprintf(t.W, "%s%s: [%s]\n", indent, n2str(n), status)
	if visited[n] && len(n.prereqs) > 0 {
		fmt.Fprintf(t.W, "%s  ...\n", indent)
		return
	}
	visited[n] = true
	for _, p := range n.prereqs {
		t.visit(status, indent+"  ", p, visited)
	}
}

func (t *StatusTool) Run(g *Graph, args []string) error {
	t.visit(Rebuild, "", g.base, make(map[*node]bool))
	return nil
}

func (t *StatusTool) String() string {
	return "status - output dependency status information"
}

type DbTool struct {
	Db *Database
	W  io.Writer
}

func (t *DbTool) Run(g *Graph, args []string) error {
	for hash, files := range t.Db.Prereqs.Hashes {
		for fname, file := range files.Data {
			fmt.Fprintf(t.W, "%016x: %s: hash=%x, time=%v, size=%d, exists=%v\n", hash, fname, file.Full, file.ModTime, file.Size, file.Exists)
		}
	}
	return nil
}

func (t *DbTool) String() string {
	return "db - show database information"
}

type PathTool struct {
	W    io.Writer
	Path string
}

func (t *PathTool) Run(g *Graph, args []string) error {
	fmt.Fprintln(t.W, t.Path)
	return nil
}

func (t *PathTool) String() string {
	return "path - return the path of the current knitfile"
}
