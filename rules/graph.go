package rules

import (
	"bytes"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/zyedidia/knit/expand"
)

// number of times a meta-rule can be used in one dependency chain.
const maxVisits = 1

type GraphSet struct {
	*Graph
	rules  map[string]*RuleSet
	graphs []*Graph
}

// Creates a new GraphSet from the map of rule sets, a specified "main"
// ruleset, a target within that ruleset, and a list of files to force as
// updated.
func NewGraphSet(rules map[string]*RuleSet, main string, target string, updated map[string]bool) (*GraphSet, error) {
	if rs, ok := rules[main]; ok {
		gs := &GraphSet{
			rules: rules,
		}
		g, err := NewGraph(rs, target, main, gs, ".", updated)
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
	rsname    string
	nodes     map[string]*node // map of targets to nodes
	fullNodes map[string]*node // map of all targets, including incidental ones, to nodes
	// Directory this graph is executed in, can be relative to the main graph.
	dir string
}

// Each node represents a build step. Certain nodes share information (e.g., if
// the two nodes build different files but use the same command), but still
// must track separate targets/prereqs, as well as the combined ones.
type node struct {
	*info
	myTarget  string
	myPrereqs []string
	myOutput  *file

	memoized   bool
	memoUpdate UpdateReason
}

type info struct {
	outputs map[string]*file
	rule    *DirectRule
	recipe  []string
	prereqs []*node

	// graph that this node belongs to
	graph *Graph

	// for cycle checking
	visited bool

	// for concurrent graph execution
	cond   *sync.Cond
	done   bool
	queued bool

	// for meta rules
	meta    bool
	match   string
	matches []string
}

// Wait until this node's condition variable is signaled.
func (n *node) wait() {
	n.cond.L.Lock()
	for !n.done {
		n.cond.Wait()
	}
	n.cond.L.Unlock()
}

// Set this node's status to done, and signal all threads waiting on the
// condition variable. This function runs when a node completes execution,
// either by finishing normally or with an error.
func (n *node) setDoneOrErr() {
	n.cond.L.Lock()
	n.done = true
	n.cond.Broadcast()
	n.cond.L.Unlock()
}

// This function is run when the node completes execution without error.
func (n *node) setDone(db *Database, noexec bool) {
	if !noexec {
		for _, p := range n.prereqs {
			for _, f := range p.outputs {
				db.Prereqs.insert(n.rule.targets, f.name, n.graph.dir)
			}
		}
		db.Recipes.insert(n.rule.targets, n.recipe, n.graph.dir)
	}
	n.setDoneOrErr()
}

// Some prereqs specify nodes from sub-builds, with the syntax
// '[ruleset][dir]prereq'. If ruleset is not empty, then this is a special
// sub-build prereq. Otherwise it is a normal prereq stored in 'name'.
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

// A file on the system. The updated field indicates that the file should be
// treated as if it was recently updated, even if it was not.
type file struct {
	name    string
	t       time.Time
	exists  bool
	updated bool
}

func newFile(dir string, target string, updated map[string]bool) *file {
	f := &file{
		name: pathJoin(dir, target),
	}
	f.updated = updated[f.name]
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

// Creates a new node that builds 'target'.
func (g *Graph) newNode(target string, updated map[string]bool) *node {
	n := &node{
		info: &info{
			outputs: map[string]*file{
				target: newFile(g.dir, target, updated),
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

func NewGraph(rs *RuleSet, target, rsname string, gs *GraphSet, dir string, updated map[string]bool) (g *Graph, err error) {
	nodes := make(map[string]*node)
	fullNodes := make(map[string]*node)
	for _, g := range gs.graphs {
		// if the graph is already in the graphset, return it instead of
		// creating a new one
		if g.dir == dir && g.rsname == rsname {
			fullNodes = g.fullNodes
			nodes = g.nodes
		}
	}

	g = &Graph{
		rs:        rs,
		rsname:    rsname,
		nodes:     nodes,
		fullNodes: fullNodes,
		dir:       dir,
	}
	gs.graphs = append(gs.graphs, g)
	visits := make([]int, len(rs.metaRules))
	g.base, err = g.resolveTarget(target, visits, gs, updated)
	if err != nil {
		return g, err
	}
	return g, checkCycles(g.base)
}

// resolveTarget returns a node that builds 'prereq'.
func (g *Graph) resolveTarget(prereq string, visits []int, gs *GraphSet, updated map[string]bool) (*node, error) {
	p := parsePrereq(prereq)
	if rs, ok := gs.rules[p.ruleset]; ok {
		// if this target uses a separate ruleset, create a subgraph and use
		// that to resolve the target.
		subg, err := NewGraph(rs, p.name, p.ruleset, gs, pathJoin(g.dir, p.dir), updated)
		if err != nil {
			return nil, err
		}
		return subg.base, nil
	}
	target := p.name

	// do we have a node that builds target already
	if n, ok := g.nodes[target]; ok {
		// make sure the node knows that it now builds target too
		if _, ok := n.outputs[target]; !ok && !n.rule.attrs.Virtual {
			n.outputs[target] = newFile(g.dir, target, updated)
		}
		return n, nil
	}
	n := g.newNode(target, updated)

	var rule DirectRule
	var ri = -1
	// do we have a direct rule available?
	ris, ok := g.rs.targets[target]
	if ok && len(ris) > 0 {
		var prereqs []string
		// Go through all the rules and accumulate all the prereqs. If multiple
		// rules have targets then we have some ambiguity, but we select the
		// last one.
		for _, ri := range ris {
			r := &g.rs.directRules[ri]
			if len(r.recipe) != 0 {
				// recipe exists -- overwrite prereqs
				prereqs = r.prereqs
			} else {
				// recipe is empty -- only add the prereqs
				prereqs = append(prereqs, r.prereqs...)
			}
			// copy over the attrs/targets/recipe into 'rule' if the currently
			// matched rule has a recipe (it is a full rule), or the
			// accumulated rule is empty.
			if len(r.recipe) != 0 || len(rule.recipe) == 0 {
				rule = *r
			}
		}
		rule.prereqs = prereqs
	} else if ok {
		// should not happen
		return nil, fmt.Errorf("internal error: target %s exists but has no rules", target)
	}

	// if we did not find a recipe from the direct rules and this target can
	// use meta-rules, then search all meta-rules for a match
	if len(rule.recipe) == 0 && !rule.attrs.NoMeta {
		// search backwards so that we get the last rule to match first, and
		// then can skip subsequent full rules, and add subsequent prereq
		// rules.
		for mi := len(g.rs.metaRules) - 1; mi >= 0; mi-- {
			mr := g.rs.metaRules[mi]
			// a meta-rule can only be used maxVisits times (in one dependency path)
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

				// there should be exactly 1 submatch (2 indices for full
				// match, 2 for the submatch) for a % match.
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
				// Is there significant performance impact from this?
				for _, p := range metarule.prereqs {
					_, err := g.resolveTarget(p, visits, gs, updated)
					if err != nil {
						failed = true
						break
					}
				}
				visits[mi]--

				if failed {
					continue
				}

				// success -- add the prereqs
				rule.prereqs = append(rule.prereqs, metarule.prereqs...)
				// overwrite the recipe/attrs/targets if the matched rule has a
				// recipe, or we don't yet have a recipe
				if len(mr.recipe) > 0 || len(rule.recipe) == 0 {
					rule.attrs = metarule.attrs
					rule.recipe = metarule.recipe
					rule.targets = []string{target}
				}

				n.meta = true
				ri = mi // for visit tracking
			}
		}
	}

	if rule.attrs.Virtual {
		n.outputs = nil
	}

	if len(rule.targets) == 0 && !rule.attrs.Virtual {
		for o, f := range n.outputs {
			if !f.exists {
				return nil, fmt.Errorf("%sno rule to make target '%s'", g.subdir(), o)
			}
		}
		// If this rule had no targets, the target is the requested one. For
		// example, maybe we didn't find a rule, and the requested target was
		// foo.c. If foo.c exists, then this is an empty rule to "build" it.
		rule.targets = []string{target}
	}

	n.myPrereqs = rule.prereqs

	// if the rule we found is equivalent to an existing rule that also builds
	// this target, then use that
	if gn, ok := g.fullNodes[target]; ok && gn.rule.Equals(&rule) {
		// make sure the node knows that it builds target too
		if _, ok := n.outputs[target]; !ok && !rule.attrs.Virtual {
			n.outputs[target] = newFile(g.dir, target, updated)
		}
		n.info = gn.info
		n.myTarget = target
		if !rule.attrs.Virtual {
			n.myOutput = newFile(g.dir, target, updated)
		}
		g.nodes[target] = n
		return n, nil
	}

	n.rule = &rule

	for _, t := range n.rule.targets {
		// TODO: maybe we should have a separate outputs map for outputs that
		// are incidentally created vs outputs that were meant to be created as
		// part of the build (incidental ones should be auto-cleaned, but only
		// actual ones should be used for build timestamping).
		if !n.rule.attrs.Virtual {
			n.outputs[t] = newFile(g.dir, t, updated)
		}
		g.fullNodes[t] = n
	}

	n.myTarget = target
	if !n.rule.attrs.Virtual {
		n.myOutput = newFile(g.dir, target, updated)
	}

	// associate this node with only the requested target
	g.nodes[target] = n

	if ri != -1 {
		visits[ri]++
	}
	for _, p := range n.rule.prereqs {
		pn, err := g.resolveTarget(p, visits, gs, updated)
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
		return fmt.Sprintf("%s: ", g.dir)
	}
	return ""
}

type VM interface {
	ExpandFuncs() (func(string) (string, error), func(string) (string, error))
	SetVar(name string, val interface{})
}

// ExpandRecipes evaluates all variables and expressions in the recipes for the
// build
func (g *Graph) ExpandRecipes(vm VM) error {
	return g.base.expandRecipe(vm)
}

func (n *node) prereqsSub(virtual bool) []string {
	ps := make([]string, 0, len(n.rule.prereqs))
	for i, prereq := range n.myPrereqs {
		p := n.prereqs[i]
		if !virtual && p.rule.attrs.Virtual {
			continue
		}
		if p.graph.dir != n.graph.dir {
			relpath, err := filepath.Rel(n.graph.dir, p.graph.dir)
			if err != nil {
				relpath = p.graph.dir
				log.Printf("error calculating relative path: %v\n", err)
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
	prs := n.prereqsSub(false)
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

type UpdateReason int

const (
	UpToDate UpdateReason = iota
	Rebuild
	NoExist
	ForceUpdate
	HashModified
	TimeModified
	RecipeModified
	Untracked
	Prereq
)

func (u UpdateReason) String() string {
	switch u {
	case UpToDate:
		return "up-to-date"
	case Rebuild:
		return "rebuild attribute"
	case NoExist:
		return "does not exist"
	case ForceUpdate:
		return "forced update"
	case HashModified:
		return "hash modified"
	case TimeModified:
		return "time modified"
	case RecipeModified:
		return "recipe modified"
	case Untracked:
		return "not in db"
	case Prereq:
		return "prereq is out-of-date"
	}
	panic("unreachable")
}

func (n *node) outOfDate(db *Database, hash bool) UpdateReason {
	if !n.memoized {
		n.memoUpdate = n.outOfDateNoMemo(db, hash)
		n.memoized = true
	}
	return n.memoUpdate
}

// returns true if this node should be rebuilt during the build
func (n *node) outOfDateNoMemo(db *Database, hash bool) UpdateReason {
	// rebuild rules are always out of date
	if n.rule.attrs.Rebuild {
		return Rebuild
	}

	// virtual rules don't have outputs
	if !n.rule.attrs.Virtual {
		// if an output does not exist, it is out of date
		for _, o := range n.outputs {
			if !o.exists {
				return NoExist
			}
		}
	}

	// if a prereq is newer than an output, this rule is out of date
	for _, p := range n.prereqs {
		for _, f := range p.outputs {
			if f.updated {
				return ForceUpdate
			}
		}

		if hash {
			if p.myOutput != nil {
				has := db.Prereqs.has(n.rule.targets, p.myOutput.name, n.graph.dir)
				if has == noHash {
					return HashModified
				} else if has == noTargets {
					return Untracked
				}
			}
		} else if p.time().After(n.time()) {
			return TimeModified
		}
	}

	// database doesn't have an entry for this recipe
	if len(n.rule.recipe) != 0 {
		has := db.Recipes.has(n.rule.targets, n.recipe, n.graph.dir)
		if has == noHash {
			return RecipeModified
		} else if has == noTargets {
			return Untracked
		}
	}

	// if a prereq is out of date, this rule is out of date
	for _, p := range n.prereqs {
		if p.outOfDate(db, hash) != UpToDate {
			return Prereq
		}
	}
	return UpToDate
}

func (n *node) count(counted map[*info]bool) int {
	s := 0
	if !counted[n.info] && len(n.rule.recipe) != 0 {
		s++
	}
	counted[n.info] = true
	for _, p := range n.prereqs {
		s += p.count(counted)
	}
	return s
}

func (g *Graph) steps() int {
	counted := make(map[*info]bool)
	return g.base.count(counted)
}
