package mak

import (
	"errors"
	"fmt"
	"log"
	"os"
	"time"
)

const maxVisits = 1

type graph struct {
	base  *node
	rs    *ruleSet
	nodes map[string]*node
}

type node struct {
	name    string
	rule    *directRule
	prereqs []*node

	// modification time
	t      time.Time
	exists bool

	// for cycle checking
	visited bool

	// for meta rules
	stem    string
	matches []string
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

func newGraph(rs *ruleSet, target string) (g *graph, err error) {
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

func (g *graph) resolveTarget(target string, visits []int) (*node, error) {
	n, ok := g.nodes[target]
	if ok {
		return n, nil
	}
	n = g.newNode(target)

	var rule directRule
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
				recipe = rule.recipe
			}
			rule = *r
			prereqs = append(prereqs, r.prereqs...)
		}
		rule.recipe = recipe
		rule.prereqs = prereqs
	} else if ok {
		log.Fatalf("error: found target with no rules")
	} else {
		for _, mr := range g.rs.metaRules {
			if mr.Match(target) {
				rule.baseRule = mr.baseRule
			}
		}
	}

	for _, p := range n.rule.Prereqs {
		pn, err := g.resolveTarget(p, visits)
		if err != nil {
			return nil, err
		}
		n.prereqs = append(n.prereqs, pn)
	}
	return n, nil
}
