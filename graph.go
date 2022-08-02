package main

import (
	"bytes"
	"errors"
	"fmt"
	"log"
	"os"
	"strings"
	"time"

	"github.com/zyedidia/gotcl"
	"github.com/zyedidia/take/expand"
)

const maxVisits = 1

type graph struct {
	base  *node
	rs    *ruleSet
	nodes map[string]*node
}

type outputSet map[string]*file

func (s outputSet) String() string {
	buf := &bytes.Buffer{}
	i := 0
	for f := range s {
		buf.WriteString(f)
		if i != len(s)-1 {
			buf.WriteByte(' ')
		}
		i++
	}
	return buf.String()
}

type node struct {
	outputs map[string]*file
	rule    *directRule
	prereqs []*node
	recipe  []string

	// for cycle checking
	visited bool

	// for meta rules
	meta    bool
	stem    string
	matches []string
}

type file struct {
	name   string
	t      time.Time
	exists bool
}

func newFile(target string) *file {
	f := &file{
		name: target,
	}
	f.updateTimestamp()
	return f
}

func (f *file) updateTimestamp() {
	info, err := os.Stat(f.name)
	if err == nil {
		f.t = info.ModTime()
		f.exists = true
		return
	}
	var perr *os.PathError
	if errors.As(err, &perr) {
		f.t = time.Unix(0, 0)
		f.exists = false
		return
	}
	log.Fatal(fmt.Errorf("update-timestamp: %w", err))
}

func (n *node) updateTimestamps() {
	for i := range n.outputs {
		n.outputs[i].updateTimestamp()
	}
}

func newGraph(rs *ruleSet, target string) (g *graph, err error) {
	g = &graph{
		rs:    rs,
		nodes: make(map[string]*node),
	}
	visits := make([]int, len(rs.metaRules))
	g.base, err = g.resolveTarget(target, visits)
	if err != nil {
		return g, err
	}
	// TODO: check ambiguity?
	return g, checkCycles(g.base)
}

func (g *graph) newNode(target string) *node {
	n := &node{
		outputs: map[string]*file{
			target: newFile(target),
		},
	}
	return n
}

func (g *graph) resolveTarget(target string, visits []int) (*node, error) {
	// do we have a node that builds target already
	n, ok := g.nodes[target]
	if ok {
		// make sure the node knows that it needs to depend on target too
		if _, ok := n.outputs[target]; !ok {
			n.outputs[target] = newFile(target)
		}
		return n, nil
	}
	n = g.newNode(target)

	var rule directRule
	var ri = -1
	// do we have a direct rule available?
	ris, ok := g.rs.targets[target]
	if ok && len(ris) > 0 {
		var prereqs []string
		var recipe []string
		for _, ri := range ris {
			r := &g.rs.directRules[ri]
			if len(r.recipe) != 0 {
				if len(recipe) != 0 {
					log.Printf("warning: multiple recipes for target %s\n", target)
				}
				recipe = r.recipe
			}
			rule = *r
			prereqs = append(prereqs, r.prereqs...)
		}
		rule.recipe = recipe
		rule.prereqs = prereqs
	} else if ok {
		log.Fatalf("error: found target with no rules")
	}
	if len(rule.recipe) == 0 && !rule.attrs.noMeta {
		for mi, mr := range g.rs.metaRules {
			if visits[mi] >= maxVisits {
				continue
			}
			if sub, pat := mr.Match(target); sub != nil {
				rule.attrs = mr.attrs
				rule.recipe = mr.recipe

				if pat.suffix && len(sub) == 4 {
					n.stem = string(target[sub[2]:sub[3]])
					for _, p := range mr.prereqs {
						idx := strings.IndexRune(p, '%')
						if idx >= 0 {
							p = strings.ReplaceAll(p, "%", n.stem)
						}
						rule.prereqs = append(rule.prereqs, p)
					}
				} else {
					for i := 0; i < len(sub); i += 2 {
						n.matches = append(n.matches, string(target[sub[i]:sub[i+1]]))
					}
					for _, p := range mr.prereqs {
						expanded := []byte{}
						expanded = pat.rgx.ExpandString(expanded, p, target, sub)
						rule.prereqs = append(rule.prereqs, string(expanded))
					}
				}
				rule.targets = []string{target}
				n.meta = true
				ri = mi
			}
		}
	}

	if len(rule.targets) == 0 {
		rule.targets = []string{target}
	}

	n.rule = &rule

	for _, t := range n.rule.targets {
		g.nodes[t] = n
	}

	if ri != -1 {
		visits[ri]++
	}
	for _, p := range n.rule.prereqs {
		pn, err := g.resolveTarget(p, visits)
		if err != nil {
			return nil, err
		}
		n.prereqs = append(n.prereqs, pn)
	}
	if ri != -1 {
		visits[ri]--
	}
	return n, nil
}

func checkCycles(n *node) error {
	if n.visited && len(n.prereqs) > 0 {
		return fmt.Errorf("cycle detected at target %v", n.outputs)
	}
	n.visited = true
	for _, p := range n.prereqs {
		if err := checkCycles(p); err != nil {
			return err
		}
	}
	n.visited = false
	return nil
}

func (n *node) time() time.Time {
	t := time.Now()
	for _, f := range n.outputs {
		if f.t.Before(t) {
			t = f.t
		}
	}
	return t
}

func (n *node) outOfDate(d *db, itp *gotcl.Interp) bool {
	if n.recipe == nil {
		err := n.expandRecipe(itp)
		if err != nil {
			log.Fatal(err)
		}
	}
	if n.rule.attrs.virtual {
		return true
	}

	for _, o := range n.outputs {
		if !o.exists {
			return true
		}
	}

	for _, p := range n.prereqs {
		if p.time().After(n.time()) {
			return true
		}
	}

	if !d.has(n.rule.targets, n.recipe) {
		return true
	}

	for _, p := range n.prereqs {
		if p.outOfDate(d, itp) {
			return true
		}
	}
	return false
}

func (n *node) expandRecipe(itp *gotcl.Interp) error {
	itp.SetVarRaw("in", gotcl.FromList(n.rule.prereqs))
	itp.SetVarRaw("out", gotcl.FromList(n.rule.targets))
	if n.meta {
		itp.SetVarRaw("stem", gotcl.FromStr(n.stem))
		for i, m := range n.matches {
			itp.SetVarRaw(fmt.Sprintf("stem%d", i), gotcl.FromStr(m))
		}
	}
	n.recipe = make([]string, 0, len(n.rule.recipe))
	for _, c := range n.rule.recipe {
		rvar, rexpr := expandFuncs(itp)
		output, err := expand.Expand(c, rvar, rexpr)
		if err != nil {
			return err
		}
		n.recipe = append(n.recipe, output)
	}
	return nil
}
