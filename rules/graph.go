package rules

import (
	"fmt"
	"io/fs"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/zyedidia/knit/expand"
)

// Number of times a meta-rule can be used in one dependency chain.
const maxVisits = 5

type Graph struct {
	base      *node
	nodes     map[string]*node // map of targets to nodes
	fullNodes map[string]*node // map of all targets, including incidental ones, to nodes

	rules *RuleSet

	// timestamp cache
	tscache map[string]time.Time
}

// Each node represents a build step. Certain nodes share information (e.g., if
// the two nodes build different files but use the same command), but still
// must track separate targets/prereqs, as well as the combined ones.
type node struct {
	*info
	myTarget  string
	myPrereqs []string
	// explicit prereqs are substituted for $input
	myExpPrereqs []string
	myOutput     *file

	memoized   [2]bool
	memoUpdate [2]UpdateReason
}

type info struct {
	outputs  map[string]*file
	rule     *DirectRule
	recipe   []string
	prereqs  []*node
	dir      string
	optional map[int]bool

	// for cycle checking
	visited  int
	expanded bool

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
func (n *node) setDone(db *Database, noexec, hash bool) {
	if !noexec {
		if hash {
			for _, p := range n.prereqs {
				for _, f := range p.outputs {
					// TODO: think about path normalization?
					db.Prereqs.insert(n.rule.targets, f.name, n.dir)
				}
			}
			if n.rule.attrs.Dep != "" {
				prereqs := loadDeps(n.dir, nil, n.rule.attrs.Dep, n.myTarget, n.optional)
				for _, p := range prereqs {
					path, err := relify(p.name)
					if err != nil {
						panic(err)
					}
					db.Prereqs.insert(n.rule.targets, path, n.dir)
				}
			}
		}
		// TODO: think about path normalization?
		db.Recipes.insert(n.rule.targets, n.recipe, n.dir)
		for _, f := range n.outputs {
			if len(n.recipe) != 0 {
				db.AddOutput(f.name)
			}
		}
	}
	n.setDoneOrErr()
}

// A file on the system. The updated field indicates that the file should be
// treated as if it was recently updated, even if it was not.
type file struct {
	name    string
	t       time.Time
	exists  bool
	updated bool
}

func newFile(target string, updated map[string]bool, tscache map[string]time.Time) *file {
	f := &file{
		name: target,
	}
	f.updated = updated[f.name]
	f.updateTimestamp(tscache)
	return f
}

func pathJoin(dir, target string) string {
	if filepath.IsAbs(target) {
		return filepath.Clean(target)
	}
	p := filepath.Join(dir, target)
	return p
}

func dirTime(dir string, mintime time.Time) time.Time {
	filepath.WalkDir(dir, func(path string, info fs.DirEntry, err error) error {
		if !info.IsDir() {
			finfo, err := os.Stat(path)
			if err == nil {
				t := finfo.ModTime()
				if t.After(mintime) {
					mintime = t
				}
			}
		}
		return nil
	})

	return mintime
}

func (f *file) updateTimestamp(timestamps map[string]time.Time) {
	if t, ok := timestamps[f.name]; ok {
		f.t = t
		f.exists = t != time.Unix(0, 0)
		return
	}

	info, err := os.Stat(f.name)
	if err == nil {
		if info.IsDir() {
			f.t = dirTime(f.name, info.ModTime())
		} else {
			f.t = info.ModTime()
		}

		f.exists = true
		timestamps[f.name] = f.t
	} else {
		f.t = time.Unix(0, 0)
		f.exists = false
	}
	timestamps[f.name] = f.t
}

// Creates a new node that builds 'target'.
func (g *Graph) newNode(target string, updated map[string]bool) *node {
	n := &node{
		info: &info{
			outputs: map[string]*file{
				target: newFile(target, updated, g.tscache),
			},
			cond:     sync.NewCond(&sync.Mutex{}),
			optional: make(map[int]bool),
		},
	}
	return n
}

func (g *Graph) Size() int {
	return len(g.nodes)
}

func NewGraph(rs *RuleSet, target string, updated map[string]bool) (g *Graph, err error) {
	g = &Graph{
		nodes:     make(map[string]*node),
		fullNodes: make(map[string]*node),
		rules:     rs,
		tscache:   make(map[string]time.Time),
	}
	visits := make([]int, len(rs.metaRules))
	g.base, err = g.resolveTarget(prereq{name: target}, visits, updated)
	if err != nil {
		return g, err
	}
	return g, checkCycles(g.base)
}

func rel(basepath, targpath string) (string, error) {
	if filepath.IsAbs(targpath) {
		var err error
		targpath, err = relify(targpath)
		if err != nil {
			return "", err
		}
	}
	return filepath.Rel(basepath, targpath)
}

// make an absolute path relative to the cwd
func relify(path string) (string, error) {
	if filepath.IsAbs(path) {
		cwd, err := os.Getwd()
		if err != nil {
			return "", err
		}
		return filepath.Rel(cwd, path)
	}
	// already relative
	return path, nil
}

func (g *Graph) resolveTarget(target prereq, visits []int, updated map[string]bool) (*node, error) {
	fulltarget, err := relify(target.name)
	if err != nil {
		return nil, err
	}

	// do we have a node that builds target already
	// if the node has an empty recipe, we don't use it because it could be a
	// candidate so we should check if we can build it in a better way
	if n, ok := g.nodes[fulltarget]; ok && len(n.rule.recipe) != 0 {
		// make sure the node knows that it now builds target too
		reltarget, err := rel(n.dir, fulltarget)
		if err != nil {
			return nil, err
		}
		if _, ok := n.outputs[reltarget]; !ok && !n.rule.attrs.Virtual {
			n.outputs[reltarget] = newFile(fulltarget, updated, g.tscache)
		}
		return n, nil
	}

	var rule DirectRule
	var expprereqs []string
	// do we have a direct rule available?
	ris, ok := g.rules.targets[fulltarget]
	if ok && len(ris) > 0 {
		var prereqs []prereq
		// Go through all the rules and accumulate all the prereqs. If multiple
		// rules have targets then we have some ambiguity, but we select the
		// last one.
		for _, ri := range ris {
			r := &g.rules.directRules[ri]
			if len(r.recipe) != 0 {
				// recipe exists -- overwrite prereqs
				prereqs = r.prereqs
				expprereqs = prereqsStr(r.prereqs, true)
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
		return nil, fmt.Errorf("internal error: target %s exists but has no rules", target.name)
	}
	var ri = -1

	n := g.newNode(fulltarget, updated)

	// if we did not find a recipe from the direct rules and this target can
	// use meta-rules, then search all meta-rules for a match
	if len(rule.recipe) == 0 && !rule.attrs.NoMeta {
		// search backwards so that we get the last rule to match first, and
		// then can skip subsequent full rules (with recipes), and add
		// subsequent prereq rules (rules without recipes).
		var curtarg string
		best := rule
		for mi := len(g.rules.metaRules) - 1; mi >= 0; mi-- {
			mr := g.rules.metaRules[mi]
			reltarget, err := rel(mr.dir, fulltarget)
			if err != nil {
				return nil, err
			}
			if sub, pat := mr.Match(reltarget); sub != nil {
				// a meta-rule can only be used maxVisits times (in one dependency path)
				// TODO: consider moving this back above the if statement so that we skip
				// the performance cost of matching if maxVisits is exceeded. In order to
				// do that, we would also need to detect whether logging is enabled, since
				// we only want to print a warning when the rule is a match.
				if visits[mi] >= maxVisits {
					log.Printf("could not use metarule '%s': exceeded max visits\n", mr.String())
					continue
				}
				// if this rule has a recipe and we already have a recipe, skip it
				if curtarg != "" && len(curtarg) <= len(reltarget) && len(mr.recipe) > 0 && len(best.recipe) > 0 {
					continue
				}

				var metarule DirectRule
				metarule.attrs = mr.attrs
				metarule.recipe = mr.recipe
				metarule.dir = mr.dir

				// there should be exactly 1 submatch (2 indices for full
				// match, 2 for the submatch) for a % match.
				if pat.Suffix && len(sub) == 4 {
					// %-metarule -- the match is the submatch and all %s in the
					// prereqs get expanded to the submatch
					n.match = string(reltarget[sub[2]:sub[3]])
					for _, p := range mr.prereqs {
						p.name = strings.ReplaceAll(p.name, "%", n.match)
						metarule.prereqs = append(metarule.prereqs, p)
					}
					metarule.attrs.Dep = strings.ReplaceAll(metarule.attrs.Dep, "%", n.match)
				} else {
					// regex match, accumulate all the matches and expand them in the prereqs
					for i := 0; i < len(sub); i += 2 {
						n.matches = append(n.matches, string(reltarget[sub[i]:sub[i+1]]))
					}
					for _, p := range mr.prereqs {
						expanded := pat.Regex.ExpandString([]byte{}, p.name, reltarget, sub)
						metarule.prereqs = append(metarule.prereqs, prereq{name: string(expanded), attrs: p.attrs})
					}
					expanded := pat.Regex.ExpandString([]byte{}, rule.attrs.Dep, reltarget, sub)
					metarule.attrs.Dep = string(expanded)
				}

				// Only use this rule if its prereqs can also be resolved.
				failed := false
				visits[mi]++
				// Is there significant performance impact from this?
				for _, p := range metarule.prereqs {
					_, err := g.resolveTarget(prereq{attrs: p.attrs, name: pathJoin(metarule.dir, p.name)}, visits, updated)
					if err != nil {
						log.Printf("could not use metarule '%s': %s\n", mr.String(), err)
						failed = true
						break
					}
				}
				visits[mi]--

				if failed {
					continue
				}

				// success -- add the prereqs
				best.dir = metarule.dir
				expprereqs = prereqsStr(metarule.prereqs, true)
				// overwrite the recipe/attrs/targets if the matched rule has a
				// recipe, or we don't yet have a recipe
				if len(mr.recipe) > 0 || len(best.recipe) == 0 {
					best.prereqs = append(rule.prereqs, metarule.prereqs...)
					best.attrs = metarule.attrs
					best.recipe = metarule.recipe
					best.targets = []string{reltarget}
				} else {
					best.prereqs = append(best.prereqs, metarule.prereqs...)
				}
				curtarg = reltarget

				n.meta = true
				ri = mi // for visit tracking
			}
		}
		rule = best
	}

	n.dir = rule.dir

	rule.attrs.UpdateFrom(target.attrs)

	if rule.attrs.Dep != "" {
		dep := pathJoin(rule.dir, rule.attrs.Dep)
		rule.prereqs = loadDeps(n.dir, rule.prereqs, dep, fulltarget, n.optional)
		n.outputs[dep] = newFile(pathJoin(n.dir, rule.attrs.Dep), updated, g.tscache)
	}

	if rule.attrs.Virtual {
		n.outputs = nil
	}

	if len(rule.targets) == 0 && !rule.attrs.Virtual {
		for o, f := range n.outputs {
			if !f.exists {
				return nil, fmt.Errorf("no rule to knit target '%s'", o)
			}
		}
		// If this rule had no targets, the target is the requested one. For
		// example, maybe we didn't find a rule, and the requested target was
		// foo.c. If foo.c exists, then this is an empty rule to "build" it.
		rule.targets = []string{fulltarget}
	}

	n.myPrereqs = prereqsStr(rule.prereqs, false)
	n.myExpPrereqs = expprereqs

	// if the rule we found is equivalent to an existing rule that also builds
	// this target, then use that
	if gn, ok := g.fullNodes[fulltarget]; ok && gn.rule.Equals(&rule) {
		// make sure the node knows that it builds target too
		reltarget, err := rel(gn.dir, fulltarget)
		if err != nil {
			return nil, err
		}
		if _, ok := n.outputs[reltarget]; !ok && !rule.attrs.Virtual {
			n.outputs[reltarget] = newFile(fulltarget, updated, g.tscache)
		}
		n.info = gn.info
		n.myTarget = reltarget
		if !rule.attrs.Virtual {
			n.myOutput = newFile(fulltarget, updated, g.tscache)
		}
		g.nodes[fulltarget] = n
		return n, nil
	}

	n.rule = &rule

	reltarget, err := rel(n.dir, fulltarget)
	if err != nil {
		return nil, err
	}

	for _, t := range n.rule.targets {
		if !n.rule.attrs.Virtual {
			n.outputs[t] = newFile(pathJoin(n.dir, t), updated, g.tscache)
		}
		g.fullNodes[pathJoin(n.dir, t)] = n
	}

	n.myTarget = reltarget
	if !n.rule.attrs.Virtual {
		n.myOutput = newFile(fulltarget, updated, g.tscache)
	}

	// associate this node with only the requested target
	g.nodes[fulltarget] = n

	if ri != -1 {
		visits[ri]++
	}
	for i, p := range n.rule.prereqs {
		pn, err := g.resolveTarget(prereq{attrs: p.attrs, name: pathJoin(n.dir, p.name)}, visits, updated)
		if err != nil {
			if n.optional[i] {
				continue
			}
			// there was an error with a prereq, so this node is invalid and we
			// must remove it from the maps
			delete(g.nodes, fulltarget)
			for _, t := range n.rule.targets {
				delete(g.fullNodes, pathJoin(n.dir, t))
			}
			return nil, err
		}
		n.prereqs = append(n.prereqs, pn)
	}

	if ri != -1 {
		visits[ri]--
	}
	return n, nil
}

func loadDeps(dir string, prereqs []prereq, depfile string, target string, opt map[int]bool) []prereq {
	dep, err := os.ReadFile(depfile)
	if err != nil {
		return prereqs
	}
	rs := NewRuleSet(dir)
	err = ParseInto(string(dep), rs, depfile, 1)
	if err != nil {
		return prereqs
	}
	ris, ok := rs.targets[target]
	if ok && len(ris) > 0 {
		for _, ri := range ris {
			r := &rs.directRules[ri]
			if len(r.recipe) != 0 {
				// recipe exists -- warn
				log.Println("warning: cannot have recipe in dep file")
			} else {
				// recipe is empty -- only add the prereqs
				for i := 0; i < len(r.prereqs); i++ {
					opt[i+len(prereqs)] = true
				}
				prereqs = append(prereqs, r.prereqs...)
			}
		}
	}
	return prereqs
}

func prereqsStr(prereqs []prereq, onlyexp bool) []string {
	exp := make([]string, 0, len(prereqs))
	for _, p := range prereqs {
		if !onlyexp || !p.attrs.Implicit {
			exp = append(exp, p.name)
		}
	}
	return exp
}
func (n *node) inputs() []string {
	ins := make([]string, 0, len(n.myPrereqs))
	for i, prereq := range n.myPrereqs {
		p := n.prereqs[i]
		if p.rule.attrs.Virtual {
			continue
		}
		ins = append(ins, prereq)
	}
	return ins
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

// Expand variable and expression references in this node's recipe. This
// function will assign the appropriate variables in the Lua VM and then
// evaluate the variables and expressions that must be expanded.
func (n *node) expandRecipe(vm VM) error {
	if n.expanded {
		return nil
	}

	prs := n.myExpPrereqs
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
	if n.rule.attrs.Dep != "" {
		vm.SetVar("dep", n.rule.attrs.Dep)
	}
	n.recipe = make([]string, 0, len(n.rule.recipe))
	for _, c := range n.rule.recipe {
		rvar, rexpr := vm.ExpandFuncs()
		output, err := expand.Expand(c, rvar, rexpr, true)
		if err != nil {
			return err
		}
		n.recipe = append(n.recipe, output)
	}

	n.expanded = true

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
	n.visited = 1
	for _, p := range n.prereqs {
		if p.visited == 1 {
			return fmt.Errorf("cycle detected at rule %v", p.rule)
		}
		if p.visited == 0 {
			if err := checkCycles(p); err != nil {
				return err
			}
		}
	}
	n.visited = 2
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
	OnlyPrereqs
	Rebuild
	NoExist
	ForceUpdate
	HashModified
	TimeModified
	RecipeModified
	Untracked
	Prereq
	LinkedUpdate
	UpToDateDynamic
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
		return "prereq hash modified"
	case TimeModified:
		return "prereq time modified"
	case RecipeModified:
		return "recipe modified"
	case Untracked:
		return "not in db"
	case Prereq:
		return "prereq is out-of-date"
	case LinkedUpdate:
		return "linked update"
	case OnlyPrereqs:
		return "only update prereqs"
	}
	panic("unreachable")
}

func (n *node) outOfDate(db *Database, hash, dynamic bool) UpdateReason {
	var i int
	if dynamic {
		i = 0
	} else {
		i = 1
	}
	if !n.memoized[i] {
		n.memoUpdate[i] = n.outOfDateNoMemo(db, hash, dynamic)
		n.memoized[i] = true
	}
	return n.memoUpdate[i]
}

// returns true if this node should be rebuilt during the build
func (n *node) outOfDateNoMemo(db *Database, hash bool, dynamic bool) UpdateReason {
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
				has := db.Prereqs.has(n.rule.targets, p.myOutput.name, n.dir)
				if has == noHash {
					return HashModified
				} else if has == noTargets {
					return Untracked
				}
			}
		} else if !p.rule.attrs.Virtual && p.time().After(n.time()) {
			log.Println(p.myTarget, "is newer than", n.myTarget)
			return TimeModified
		}
	}

	// database doesn't have an entry for this recipe
	if len(n.rule.recipe) != 0 {
		has := db.Recipes.has(n.rule.targets, n.recipe, n.dir)
		if has == noHash {
			return RecipeModified
		} else if has == noTargets {
			return Untracked
		}
	}

	// if a prereq is out of date, this rule is out of date
	order := false
	for _, p := range n.prereqs {
		ood := p.outOfDate(db, hash, false)
		// if the only prereqs out of date are order-only, then we just run
		// them but this rule does not need to rebuild
		if !p.rule.attrs.Order && ood != UpToDate && ood != OnlyPrereqs {
			if dynamic {
				return UpToDateDynamic
			}
			return Prereq
		}
		if ood != UpToDate {
			order = true
		}
	}
	if order {
		return OnlyPrereqs
	}
	return UpToDate
}

func (n *node) count(db *Database, full, hash bool, counted map[*info]bool) int {
	s := 0
	ood := n.outOfDate(db, hash, false)
	if !full && ood == UpToDate {
		return 0
	}
	if ood != OnlyPrereqs && len(n.rule.recipe) != 0 {
		s++
	}
	counted[n.info] = true
	for _, p := range n.prereqs {
		if counted[p.info] {
			continue
		}
		s += p.count(db, full, hash, counted)
	}
	return s
}

func (g *Graph) steps(db *Database, full, hash bool) int {
	counted := make(map[*info]bool)
	return g.base.count(db, full, hash, counted)
}
