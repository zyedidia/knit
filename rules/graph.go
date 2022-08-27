package rules

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/zyedidia/knit/expand"
)

const maxVisits = 1

type GraphSet struct {
	*Graph
	rules  map[string]*RuleSet
	graphs []*Graph
}

func NewGraphSet(rules map[string]*RuleSet, main string, target string) (*GraphSet, error) {
	if rs, ok := rules[main]; ok {
		gs := &GraphSet{
			rules: rules,
		}
		g, err := NewGraph(rs, target, gs, main, ".")
		if err != nil {
			return nil, err
		}
		gs.Graph = g
		return gs, nil
	}
	return nil, fmt.Errorf("ruleset not found: %s", main)
}

type Graph struct {
	base      *node
	rs        *RuleSet
	nodes     map[string]*node
	fullNodes map[string]*node
	// Directory this graph is executed in, can be relative to the main graph.
	dir string
}

type inner struct {
	outputs map[string]*file
	rule    *DirectRule
	recipe  []string
	prereqs []*node

	// graph that this node belongs to
	graph *Graph

	// for cycle checking
	visited bool

	// for meta rules
	meta    bool
	match   string
	matches []string

	// for concurrent graph execution
	cond   *sync.Cond
	done   bool
	queued bool

	// for build step count estimate
	counted bool
}

type node struct {
	*inner
	myPrereqs []string
}

func (n *node) wait() {
	n.cond.L.Lock()
	for !n.done {
		n.cond.Wait()
	}
	n.cond.L.Unlock()
}

func (n *node) setDone() {
	n.cond.L.Lock()
	n.done = true
	n.cond.Broadcast()
	n.cond.L.Unlock()
}

type prereq struct {
	name    string
	ruleset string
	dir     string
}

func parsePrereq(ps string) prereq {
	var p prereq
	buf := &bytes.Buffer{}
	sawset := false
	sawdir := false
	sqn := 0

	pos := 0
	for pos < len(ps) {
		r, size := utf8.DecodeRuneInString(ps[pos:])
		switch r {
		case '[':
			sqn++
		case ']':
			sqn--
			if sqn == 0 {
				if sawset && sawdir {
					buf.WriteRune(r)
				} else if sawset {
					p.dir = buf.String()
					buf.Reset()
					sawdir = true
				} else {
					p.ruleset = buf.String()
					buf.Reset()
					sawset = true
				}
			}
		default:
			buf.WriteRune(r)
		}
		pos += size
	}
	p.name = buf.String()
	return p
}

type file struct {
	name   string
	t      time.Time
	exists bool
}

func newFile(dir string, target string) *file {
	f := &file{
		name: pathJoin(dir, target),
	}
	f.updateTimestamp()
	return f
}

func pathJoin(dir, target string) string {
	if filepath.IsAbs(target) {
		return target
	}
	return filepath.Join(dir, target)
}

func (f *file) updateTimestamp() {
	info, err := os.Stat(f.name)
	if err == nil {
		f.t = info.ModTime()
		f.exists = true
		return
	}
	f.t = time.Unix(0, 0)
	f.exists = false
}

func (f *file) remove() error {
	return os.RemoveAll(f.name)
}

func (g *Graph) newNode(target string) *node {
	n := &node{
		inner: &inner{
			outputs: map[string]*file{
				target: newFile(g.dir, target),
			},
			graph: g,
			cond:  sync.NewCond(&sync.Mutex{}),
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

func NewGraph(rs *RuleSet, target string, gs *GraphSet, name string, dir string) (g *Graph, err error) {
	g = &Graph{
		rs:        rs,
		nodes:     make(map[string]*node),
		fullNodes: make(map[string]*node),
		dir:       dir,
	}
	gs.graphs = append(gs.graphs, g)
	visits := make([]int, len(rs.metaRules))
	g.base, err = g.resolveTarget(target, visits, gs)
	if err != nil {
		return g, err
	}
	return g, checkCycles(g.base)
}

func (g *Graph) resolveTarget(target string, visits []int, gs *GraphSet) (*node, error) {
	p := parsePrereq(target)
	if rs, ok := gs.rules[p.ruleset]; ok {
		// if this target uses a separate ruleset, create a subgraph and
		// use that to resolve the target.
		subg, err := NewGraph(rs, p.name, gs, p.ruleset, pathJoin(g.dir, p.dir))
		if err != nil {
			return nil, err
		}
		return subg.base, nil
	}
	target = p.name

	// do we have a node that builds target already
	n, ok := g.nodes[target]
	if ok {
		// make sure the node knows that it builds target too
		if _, ok := n.outputs[target]; !ok {
			n.outputs[target] = newFile(g.dir, target)
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
				recipe = r.recipe
				prereqs = r.prereqs
			} else {
				prereqs = append(prereqs, r.prereqs...)
			}
			rule = *r
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
				// if this rule has a recipe and we already have a recipe, skip it
				if len(mr.recipe) > 0 && len(rule.recipe) > 0 {
					continue
				}

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
						expanded := pat.Regex.ExpandString([]byte{}, p, target, sub)
						metarule.prereqs = append(metarule.prereqs, string(expanded))
					}
				}

				// Only use this rule if its prereqs can also be resolved.
				failed := false
				visits[mi]++
				// TODO: measure the performance impact of this, and optimize if necessary
				for _, p := range metarule.prereqs {
					_, err := g.resolveTarget(p, visits, gs)
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
			}
		}
	}

	if len(rule.targets) == 0 && !rule.attrs.Virtual {
		for o, f := range n.outputs {
			if !f.exists {
				return nil, fmt.Errorf("%sno rule to make target '%s'", g.subdir(), o)
			}
		}
		// If this rule had no targets, the target is the requested one.  For
		// example, maybe we didn't find a rule, and the requested target was
		// foo.c. If foo.c exists, then this is an empty rule to "build" it.
		rule.targets = []string{target}
	}

	n.myPrereqs = rule.prereqs

	// if the rule we found is equivalent to an existing rule that also builds
	// this target, then use that
	if gn, ok := g.fullNodes[target]; ok && gn.rule.Equals(&rule) {
		// make sure the node knows that it builds target too
		if _, ok := n.outputs[target]; !ok {
			n.outputs[target] = newFile(g.dir, target)
		}
		n.inner = gn.inner
		g.nodes[target] = n
		return n, nil
	}

	n.rule = &rule

	for _, t := range n.rule.targets {
		g.fullNodes[t] = n
	}

	n.rule.targets = []string{target}

	// associate this node with only the requested target
	g.nodes[target] = n

	if ri != -1 {
		visits[ri]++
	}
	for _, p := range n.rule.prereqs {
		pn, err := g.resolveTarget(p, visits, gs)
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

func (g *Graph) subdir() string {
	if g.dir != "." && g.dir != "" {
		return fmt.Sprintf("in %s: ", g.dir)
	}
	return ""
}

// ExpandRecipes evaluates all variables and expressions in the recipes for the
// build
func (g *Graph) ExpandRecipes(vm VM) error {
	return g.base.expandRecipe(vm)
}

func (n *node) prereqsSub() []string {
	ps := make([]string, 0, len(n.rule.prereqs))
	for i, prereq := range n.myPrereqs {
		p := n.prereqs[i]
		if p.rule.attrs.Virtual {
			continue
		}
		if p.graph.dir != n.graph.dir {
			relpath, err := filepath.Rel(n.graph.dir, p.graph.dir)
			if err != nil {
				// TODO
				panic(err)
			}
			ps = append(ps, pathJoin(relpath, parsePrereq(prereq).name))
		} else {
			ps = append(ps, prereq)
		}
	}
	return ps
}

// Expand variable and expression references in this node's recipe. This
// function will assign the appropriate variables in the Lua VM and then
// evaluate the variables and expressions that must be expanded.
func (n *node) expandRecipe(vm VM) error {
	prs := n.prereqsSub()
	vm.SetVar("inputs", prs)
	vm.SetVar("input", strings.Join(prs, " "))
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

	for _, pn := range n.prereqs {
		err := pn.expandRecipe(vm)
		if err != nil {
			return err
		}
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
	// rebuild rules are always out of date
	if n.rule.attrs.Rebuild {
		return true
	}

	// virtual rules don't have outputs
	if !n.rule.attrs.Virtual {
		// if an output does not exist, it is out of date
		for _, o := range n.outputs {
			if !o.exists {
				return true
			}
		}

		// if a prereq is newer than an output, this rule is out of date
		for _, p := range n.prereqs {
			if p.time().After(n.time()) {
				return true
			}
		}
	}

	// database doesn't have an entry for this recipe
	if !db.HasRecipe(n.rule.targets, n.recipe, n.graph.dir) {
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

func (g *Graph) VisualizeDot(w io.Writer) {
	fmt.Fprintln(w, "digraph take {")
	g.base.visualizeDot(w)
	fmt.Fprintln(w, "}")
}

func (n *node) String() string {
	if n.graph.dir == "" || n.graph.dir == "." {
		return strings.Join(n.rule.targets, ", ")
	}
	return fmt.Sprintf("[%s]%s", n.graph.dir, strings.Join(n.rule.targets, ", "))
}

func (n *node) visualizeDot(w io.Writer) {
	for _, p := range n.prereqs {
		fmt.Fprintf(w, "    \"%s\" -> \"%s\";\n", n, p)
		p.visualizeDot(w)
	}
}

func (g *Graph) VisualizeText(w io.Writer) {
	g.base.visualizeText(w)
}

func (n *node) visualizeText(w io.Writer) {
	for _, p := range n.prereqs {
		fmt.Fprintf(w, "%s -> %s\n", n, p)
		p.visualizeText(w)
	}
}

// don't want to count virtual rules for clean counts
func (n *node) count(virtual bool) int {
	s := 0
	if !n.counted && len(n.rule.recipe) != 0 {
		if virtual || (!virtual && !n.rule.attrs.Virtual) {
			s++
		}
	}
	n.counted = true
	for _, p := range n.prereqs {
		s += p.count(virtual)
	}
	return s
}

func (g *Graph) Steps(virtual bool) int {
	return g.base.count(virtual)
}
