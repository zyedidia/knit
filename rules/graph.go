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

// number of times a meta-rule can be used in one dependency chain.
const maxVisits = 5

func sub(dir string) string {
	if dir == "." || dir == "" {
		return ""
	}
	return fmt.Sprintf("%s: ", dir)
}

type Graph struct {
	base      *node
	nodes     map[string]*node // map of targets to nodes
	fullNodes map[string]*node // map of all targets, including incidental ones, to nodes

	rsets map[string]*RuleSet
	dirs  []string

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

	memoized   bool
	memoUpdate UpdateReason
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
		}
		// TODO: think about path normalization?
		db.Recipes.insert(n.rule.targets, n.recipe, n.dir)
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

func newFile(dir string, target string, updated map[string]bool, tscache map[string]time.Time) *file {
	f := &file{
		name: pathJoin(dir, target),
	}
	f.updated = updated[f.name]
	f.updateTimestamp(tscache)
	return f
}

func pathJoin(dir, target string) string {
	if filepath.IsAbs(target) {
		return target
	}
	p := filepath.Join(dir, target)
	if strings.HasSuffix(target, "/") {
		p += "/"
	}
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

func (f *file) remove() error {
	return os.RemoveAll(f.name)
}

// Creates a new node that builds 'target'.
func (g *Graph) newNode(target string, dir string, updated map[string]bool) *node {
	n := &node{
		info: &info{
			outputs: map[string]*file{
				target: newFile(dir, target, updated, g.tscache),
			},
			dir:      dir,
			cond:     sync.NewCond(&sync.Mutex{}),
			optional: make(map[int]bool),
		},
	}
	return n
}

func (g *Graph) Size() int {
	return len(g.nodes)
}

func NewGraph(rs map[string]*RuleSet, dirs []string, target string, updated map[string]bool) (g *Graph, err error) {
	g = &Graph{
		nodes:     make(map[string]*node),
		fullNodes: make(map[string]*node),
		rsets:     rs,
		dirs:      dirs,
		tscache:   make(map[string]time.Time),
	}
	visits := make(map[string][]int)
	for d, r := range rs {
		visits[d] = make([]int, len(r.metaRules))
	}
	g.base, err = g.resolveTargetAcross(target, visits, updated)
	if err != nil {
		return g, err
	}
	return g, checkCycles(g.base)
}

func rel(basepath, targpath string) (string, error) {
	if filepath.IsAbs(targpath) {
		p, err := filepath.Abs(basepath)
		if err != nil {
			return "", err
		}
		basepath = p
	}
	ps := string(os.PathSeparator)
	slash := strings.HasSuffix(targpath, ps)
	rel, err := filepath.Rel(basepath, targpath)
	if err != nil {
		return rel, err
	}
	if slash {
		rel += ps
	}
	return rel, err
}

// Resolves 'target' by looking across all rulesets.
func (g *Graph) resolveTargetAcross(target string, visits map[string][]int, updated map[string]bool) (*node, error) {
	dir := filepath.Dir(target)

	var candidate *node

	var rerr error
	if rs, ok := g.rsets[dir]; ok {
		rel, err := rel(dir, target)
		if err != nil {
			return nil, err
		}
		n, err := g.resolveTargetForRuleSet(rs, dir, rel, visits, updated)
		if err == nil {
			if len(n.rule.recipe) != 0 {
				return n, nil
			}
			candidate = n
		}
		rerr = err
	}

	for _, d := range g.dirs {
		if d == dir {
			continue
		}
		rel, err := rel(d, target)
		if err != nil {
			return nil, err
		}
		n, err := g.resolveTargetForRuleSet(g.rsets[d], d, rel, visits, updated)
		if err == nil {
			if len(n.rule.recipe) != 0 {
				return n, nil
			}
			if candidate == nil {
				candidate = n
			}
		}
		if rerr == nil {
			rerr = err
		}
	}

	if candidate != nil {
		return candidate, nil
	}

	return nil, rerr
}

func loadDeps(prereqs []string, depfile string, target string, opt map[int]bool) []string {
	dep, err := os.ReadFile(depfile)
	if err != nil {
		return prereqs
	}
	rs := NewRuleSet()
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

func (g *Graph) resolveTargetForRuleSet(rs *RuleSet, dir string, target string, visits map[string][]int, updated map[string]bool) (*node, error) {
	parsedTarget, err := parsePrereq(target)
	if err != nil {
		return nil, err
	}
	target = parsedTarget.name

	fulltarget := pathJoin(dir, target)
	// do we have a node that builds target already
	// if the node has an empty recipe, we don't use it because it could be a
	// candidate so we should check if we can build it in a better way
	if n, ok := g.nodes[fulltarget]; ok && len(n.rule.recipe) != 0 && n.dir == dir {
		// make sure the node knows that it now builds target too
		if _, ok := n.outputs[target]; !ok && !n.rule.attrs.Virtual {
			n.outputs[target] = newFile(dir, target, updated, g.tscache)
		}
		return n, nil
	}
	n := g.newNode(target, dir, updated)
	var rule DirectRule
	var expprereqs []string
	// do we have a direct rule available?
	ris, ok := rs.targets[target]
	if ok && len(ris) > 0 {
		var prereqs []string
		// Go through all the rules and accumulate all the prereqs. If multiple
		// rules have targets then we have some ambiguity, but we select the
		// last one.
		for _, ri := range ris {
			r := &rs.directRules[ri]
			if len(r.recipe) != 0 {
				// recipe exists -- overwrite prereqs
				prereqs = r.prereqs
				expprereqs = explicits(r.prereqs)
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
	var ri = -1

	// if we did not find a recipe from the direct rules and this target can
	// use meta-rules, then search all meta-rules for a match
	if len(rule.recipe) == 0 && !rule.attrs.NoMeta {
		// search backwards so that we get the last rule to match first, and
		// then can skip subsequent full rules, and add subsequent prereq
		// rules.
		for mi := len(rs.metaRules) - 1; mi >= 0; mi-- {
			mr := rs.metaRules[mi]
			if sub, pat := mr.Match(target); sub != nil {
				// a meta-rule can only be used maxVisits times (in one dependency path)
				// TODO: consider moving this back above the if statement so that we skip
				// the performance cost of matching if maxVisits is exceeded. In order to
				// do that, we would also need to detect whether logging is enabled, since
				// we only want to print a warning when the rule is a match.
				if visits[dir][mi] >= maxVisits {
					log.Printf("could not use metarule '%s': exceeded max visits\n", mr.String())
					continue
				}
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
					metarule.attrs.Dep = strings.ReplaceAll(metarule.attrs.Dep, "%", n.match)
				} else {
					// regex match, accumulate all the matches and expand them in the prereqs
					for i := 0; i < len(sub); i += 2 {
						n.matches = append(n.matches, string(target[sub[i]:sub[i+1]]))
					}
					for _, p := range mr.prereqs {
						expanded := pat.Regex.ExpandString([]byte{}, p, target, sub)
						metarule.prereqs = append(metarule.prereqs, string(expanded))
					}
					expanded := pat.Regex.ExpandString([]byte{}, rule.attrs.Dep, target, sub)
					metarule.attrs.Dep = string(expanded)
				}

				// Only use this rule if its prereqs can also be resolved.
				failed := false
				visits[dir][mi]++
				// Is there significant performance impact from this?
				for _, p := range metarule.prereqs {
					_, err := g.resolveTargetAcross(pathJoin(dir, p), visits, updated)
					if err != nil {
						log.Printf("could not use metarule '%s': %s\n", mr.String(), err)
						failed = true
						break
					}
				}
				visits[dir][mi]--

				if failed {
					continue
				}

				// success -- add the prereqs
				rule.prereqs = append(rule.prereqs, metarule.prereqs...)
				expprereqs = explicits(metarule.prereqs)
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

	rule.attrs.UpdateFrom(parsedTarget.attrs)

	if rule.attrs.Dep != "" {
		rule.prereqs = loadDeps(rule.prereqs, filepath.Join(dir, rule.attrs.Dep), target, n.optional)
	}

	if rule.attrs.Virtual {
		n.outputs = nil
	}

	if len(rule.targets) == 0 && !rule.attrs.Virtual {
		for o, f := range n.outputs {
			if !f.exists {
				return nil, fmt.Errorf("%sno rule to knit target '%s'", sub(dir), o)
			}
		}
		// If this rule had no targets, the target is the requested one. For
		// example, maybe we didn't find a rule, and the requested target was
		// foo.c. If foo.c exists, then this is an empty rule to "build" it.
		rule.targets = []string{target}
	}

	n.myPrereqs = rule.prereqs
	n.myExpPrereqs = expprereqs

	// if the rule we found is equivalent to an existing rule that also builds
	// this target, then use that
	if gn, ok := g.fullNodes[fulltarget]; ok && gn.rule.Equals(&rule) {
		// make sure the node knows that it builds target too
		if _, ok := n.outputs[target]; !ok && !rule.attrs.Virtual {
			n.outputs[target] = newFile(dir, target, updated, g.tscache)
		}
		n.info = gn.info
		n.myTarget = target
		if !rule.attrs.Virtual {
			n.myOutput = newFile(dir, target, updated, g.tscache)
		}
		g.nodes[fulltarget] = n
		return n, nil
	}

	n.rule = &rule

	for _, t := range n.rule.targets {
		if !n.rule.attrs.Virtual {
			n.outputs[t] = newFile(dir, t, updated, g.tscache)
		}
		g.fullNodes[pathJoin(dir, t)] = n
	}

	n.myTarget = target
	if !n.rule.attrs.Virtual {
		n.myOutput = newFile(dir, target, updated, g.tscache)
	}

	// associate this node with only the requested target
	g.nodes[fulltarget] = n

	if ri != -1 {
		visits[dir][ri]++
	}
	for i, p := range n.rule.prereqs {
		pn, err := g.resolveTargetAcross(pathJoin(dir, p), visits, updated)
		if err != nil {
			if n.optional[i] {
				continue
			}
			// there was an error with a prereq, so this node is invalid and we
			// must remove it from the maps
			delete(g.nodes, fulltarget)
			for _, t := range n.rule.targets {
				delete(g.fullNodes, pathJoin(dir, t))
			}
			return nil, err
		}
		// If we got a meta-rule out that comes from a different ruleset, try
		// resolving using the current ruleset. If that doesn't work, just use
		// the other one, but for meta-rules we should always try to resolve
		// using the current ruleset over a different ruleset.
		if pn.meta && pn.dir != dir {
			internaln, err := g.resolveTargetForRuleSet(rs, dir, p, visits, updated)
			if err == nil {
				pn = internaln
			}
		}
		n.prereqs = append(n.prereqs, pn)
	}

	if ri != -1 {
		visits[dir][ri]--
	}
	return n, nil
}

func explicits(prereqs []string) []string {
	exp := make([]string, 0, len(prereqs))
	for _, p := range prereqs {
		parsed, _ := parsePrereq(p)
		if !parsed.attrs.Implicit {
			exp = append(exp, parsed.name)
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
				has := db.Prereqs.has(n.rule.targets, p.myOutput.name, n.dir)
				if has == noHash {
					return HashModified
				} else if has == noTargets {
					return Untracked
				}
			}
		} else if !p.rule.attrs.Virtual && p.time().After(n.time()) {
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
		ood := p.outOfDate(db, hash)
		// if the only prereqs out of date are order-only, then we just run
		// them but this rule does not need to rebuild
		if !p.rule.attrs.Order && ood != UpToDate && ood != OnlyPrereqs {
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
	ood := n.outOfDate(db, hash)
	if !full && n.outOfDate(db, hash) == UpToDate {
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
