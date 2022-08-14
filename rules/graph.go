package rules

import (
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"strings"
	"time"

	"github.com/zyedidia/knit/expand"
)

const maxVisits = 1

type Graph struct {
	base  *node
	rs    *RuleSet
	nodes map[string]*node
}

type node struct {
	outputs map[string]*file
	rule    *DirectRule
	prereqs []*node
	recipe  []string

	// for cycle checking
	visited bool

	// for meta rules
	meta    bool
	match   string
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
	// not sure what happened in this case
	log.Fatalf("update-timestamp: %v\n", err)
}

func (g *Graph) newNode(target string) *node {
	n := &node{
		outputs: map[string]*file{
			target: newFile(target),
		},
	}
	return n
}

func (g *Graph) Size() int {
	return len(g.nodes)
}

type VM interface {
	ExpandFuncs() (func(string) (string, error), func(string) (string, error))
	SetVar(name string, val interface{})
}

func NewGraph(rs *RuleSet, target string) (g *Graph, err error) {
	g = &Graph{
		rs:    rs,
		nodes: make(map[string]*node),
	}
	visits := make([]int, len(rs.metaRules))
	g.base, err = g.resolveTarget(target, visits)
	if err != nil {
		return g, err
	}
	return g, checkCycles(g.base)
}

func (g *Graph) resolveTarget(target string, visits []int) (*node, error) {
	// do we have a node that builds target already
	n, ok := g.nodes[target]
	if ok {
		// make sure the node knows that it builds target too
		if _, ok := n.outputs[target]; !ok {
			n.outputs[target] = newFile(target)
		}
		return n, nil
	}
	n = g.newNode(target)

	var rule DirectRule
	var ri = -1
	// do we have a direct rule available?
	ris, ok := g.rs.targets[target]
	if ok && len(ris) > 0 {
		var prereqs []string
		var recipe []string
		// Go through all the rules and accumulate all the prereqs. If multiple
		// rules have targets then we have some ambiguity.
		for _, ri := range ris {
			r := &g.rs.directRules[ri]
			if len(r.recipe) != 0 {
				// if len(recipe) != 0 {
				// 	return nil, fmt.Errorf("multiple recipes found for target '%s'", target)
				// }
				recipe = r.recipe
			}
			rule = *r
			prereqs = append(prereqs, r.prereqs...)
		}
		rule.recipe = recipe
		rule.prereqs = prereqs
	} else if ok {
		return nil, fmt.Errorf("internal error: target %s exists but has no rules", target)
	}
	// if we did not find a recipe from the direct rules and this target can
	// use meta-rules, then search all meta-rules for a match
	if len(rule.recipe) == 0 && !rule.attrs.NoMeta {
		for mi := len(g.rs.metaRules) - 1; mi >= 0; mi-- {
			mr := g.rs.metaRules[mi]
			// a meta-rule can only be used maxVisits times
			if visits[mi] >= maxVisits {
				continue
			}
			if sub, pat := mr.Match(target); sub != nil {
				var metarule DirectRule
				metarule.attrs = mr.attrs
				metarule.recipe = mr.recipe

				if pat.Suffix && len(sub) == 4 {
					// %-metarule -- the match is the submatch and all %s in the
					// prereqs get expanded to the submatch
					n.match = string(target[sub[2]:sub[3]])
					for _, p := range mr.prereqs {
						p = strings.ReplaceAll(p, "%", n.match)
						metarule.prereqs = append(metarule.prereqs, p)
					}
				} else {
					// regex match, accumulate all the matches and expand them in the prereqs
					for i := 0; i < len(sub); i += 2 {
						n.matches = append(n.matches, string(target[sub[i]:sub[i+1]]))
					}
					for _, p := range mr.prereqs {
						expanded := pat.Rgx.ExpandString([]byte{}, p, target, sub)
						metarule.prereqs = append(metarule.prereqs, string(expanded))
					}
				}

				failed := false
				visits[mi]++
				// TODO: measure the performance impact of this, and optimize if necessary
				for _, p := range metarule.prereqs {
					_, err := g.resolveTarget(p, visits)
					if err != nil {
						failed = true
						break
					}
				}
				visits[mi]--

				if failed {
					continue
				}

				rule.prereqs = append(rule.prereqs, metarule.prereqs...)
				rule.attrs = metarule.attrs
				rule.recipe = metarule.recipe

				rule.targets = []string{target}
				n.meta = true
				ri = mi // for visit tracking
				break
			}
		}
	}

	if len(rule.targets) == 0 && !rule.attrs.Virtual {
		for o, f := range n.outputs {
			if !f.exists {
				return nil, fmt.Errorf("no rule to make target '%s'", o)
			}
		}
		rule.targets = []string{target}
	}

	n.rule = &rule

	// associate this node with all of the matched rule's targets
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

// ExpandRecipes evaluates all variables and expressions in the recipes for the
// build
func (g *Graph) ExpandRecipes(vm VM) error {
	for _, n := range g.nodes {
		if n.recipe == nil {
			err := n.expandRecipe(vm)
			if err != nil {
				return err
			}
		}
	}
	return nil
}

// Expand variable and expression references in this node's recipe. This
// function will assign the appropriate variables in the Lua VM and then
// evaluate the variables and expressions that must be expanded.
func (n *node) expandRecipe(vm VM) error {
	vm.SetVar("inputs", n.rule.prereqs)
	vm.SetVar("input", strings.Join(n.rule.prereqs, " "))
	vm.SetVar("outputs", n.rule.targets)
	vm.SetVar("output", strings.Join(n.rule.targets, " "))
	if n.meta {
		vm.SetVar("match", n.match)
		for i, m := range n.matches {
			vm.SetVar(fmt.Sprintf("match%d", i), m)
		}
		vm.SetVar("matches", n.matches)
	}
	n.recipe = make([]string, 0, len(n.rule.recipe))
	for _, c := range n.rule.recipe {
		rvar, rexpr := vm.ExpandFuncs()
		output, err := expand.Expand(c, rvar, rexpr)
		if err != nil {
			return err
		}
		n.recipe = append(n.recipe, output)
	}
	return nil
}

// checks the graph for cycles starting at node n
func checkCycles(n *node) error {
	if n.visited && len(n.prereqs) > 0 {
		return fmt.Errorf("cycle detected at rule %v", n.rule)
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

// returns the last modified time for the oldest output of this node
func (n *node) time() time.Time {
	t := time.Now()
	for _, f := range n.outputs {
		if f.t.Before(t) {
			t = f.t
		}
	}
	return t
}

// returns true if this node should be rebuilt during the build
func (n *node) outOfDate(db *Database) bool {
	// virtual rules are always out of date
	if n.rule.attrs.Virtual || n.rule.attrs.Rebuild {
		return true
	}
	// if an output does not exist, it is out of date
	for _, o := range n.outputs {
		if !o.exists {
			return true
		}
	}

	// if a prereq is newer than an output, this rule is out of date
	for _, p := range n.prereqs {
		// if the times are exactly the same we also consider this out-of-date
		if p.time().After(n.time()) || p.time() == n.time() {
			return true
		}
	}

	// database doesn't have an entry for this recipe
	if !db.HasRecipe(n.rule.targets, n.recipe) {
		return true
	}

	// if a prereq is out of date, this rule is out of date
	for _, p := range n.prereqs {
		if p.outOfDate(db) {
			return true
		}
	}
	return false
}

func (g *Graph) Visualize(w io.Writer) {
	fmt.Fprintln(w, "digraph take {")
	g.base.visualize(w)
	fmt.Fprintln(w, "}")
}

func (n *node) visualize(w io.Writer) {
	for _, p := range n.prereqs {
		fmt.Fprintf(w, "    \"%s\" -> \"%s\";\n", strings.Join(n.rule.targets, ", "), strings.Join(p.rule.targets, ", "))
		p.visualize(w)
	}
}
