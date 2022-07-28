package main

import (
	"errors"
	"fmt"
	"io"
	"log"
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

func (n *node) updateTimestamp() {
	info, err := os.Stat(n.name)
	if err == nil {
		n.t = info.ModTime()
		n.exists = true
		return
	}
	var perr *os.PathError
	if errors.As(err, &perr) {
		n.t = time.Unix(0, 0)
		n.exists = false
		return
	}
	log.Fatal(fmt.Errorf("update-timestamp: %w", err))
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
	n.updateTimestamp()
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
			n.rule = &Rule{
				Targets: []Pattern{{
					str: target,
				}},
			}
			return n, nil
		}
	} else {
		n.rule = &Rule{
			Targets: []Pattern{{
				str: target,
			}},
		}
		return n, nil
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

func (n *node) outOfDate() bool {
	if n.rule.Attrs.Virtual {
		return true
	}

	for _, p := range n.prereqs {
		if p.t.After(n.t) {
			return true
		}
	}
	for _, p := range n.prereqs {
		if p.outOfDate() {
			return true
		}
	}
	return false
}
