package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"time"
)

const maxVisits = 1

type graph struct {
	base  *node
	rs    *RuleSet
	nodes map[string]*node
}

type node struct {
	name    string
	rule    *Rule
	prereqs []*node

	// modification time
	t      time.Time
	exists bool

	// for cycle checking
	visited bool
}

func (n *node) updateTimestamp() error {
	info, err := os.Stat(n.name)
	if err == nil {
		n.t = info.ModTime()
		n.exists = true
		return nil
	}
	var perr os.PathError
	if errors.Is(err, &perr) {
		n.t = time.Unix(0, 0)
		n.exists = false
	}
	return err
}

func newGraph(rs *RuleSet, target string) (g *graph, err error) {
	g = &graph{
		rs:    rs,
		nodes: make(map[string]*node),
	}
	visits := make([]int, len(rs.Rules))
	g.base, err = g.resolveTarget(target, visits)
	if err != nil {
		return g, err
	}
	// TODO: check ambiguity
	// TODO: vacuousness, etc.
	return g, checkCycles(g.base)
}

func (g *graph) newNode(target string) *node {
	n := &node{
		name: target,
	}
	g.nodes[target] = n
	return n
}

// Print a graph in graphviz format.
func (g *graph) visualize(w io.Writer) {
	fmt.Fprintln(w, "digraph mk {")
	for t, n := range g.nodes {
		for i := range n.prereqs {
			if n.prereqs[i] != nil {
				fmt.Fprintf(w, "    \"%s\" -> \"%s\";\n", t, n.prereqs[i].name)
			}
		}
	}
	fmt.Fprintln(w, "}")
}

func (g *graph) resolveTarget(target string, visits []int) (*node, error) {
	n, ok := g.nodes[target]
	if ok {
		return n, nil
	}
	n = g.newNode(target)

	// figure out which rule to use for this node
	var ni int
	ris, ok := g.rs.Targets[target]
	if ok {
		for _, ri := range ris {
			r := &g.rs.Rules[ri]
			if visits[ri] < maxVisits && !r.Meta {
				ni = ri
				n.rule = r
				break
			}
		}
		if n.rule == nil {
			return nil, fmt.Errorf("could not find target: %s", target)
		}
	} else {
		return nil, fmt.Errorf("could not find target: %s", target)
	}

	visits[ni]++
	for _, p := range n.rule.Prereqs {
		pn, err := g.resolveTarget(p, visits)
		if err != nil {
			return nil, err
		}
		n.prereqs = append(n.prereqs, pn)
	}
	visits[ni]--
	return n, nil
}

func checkCycles(n *node) error {
	if n.visited && len(n.prereqs) > 0 {
		return fmt.Errorf("cycle detected at target %s", n.name)
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

func checkAmbiguity(n *node) error {
	return nil
}
