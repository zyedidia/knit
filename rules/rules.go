package rules

import (
	"bytes"
	"fmt"
	"regexp"
	"strings"
)

type Rule interface {
	isRule()
}

type baseRule struct {
	prereqs []prereq
	attrs   AttrSet
	recipe  []string
	dir     string
}

func (b baseRule) isRule() {}

func (b *baseRule) prereqsString() string {
	buf := &bytes.Buffer{}
	for i, p := range b.prereqs {
		buf.WriteString(p.name)
		if i != len(b.prereqs)-1 {
			buf.WriteByte(' ')
		}
	}
	return buf.String()
}

// A DirectRule specifies how to build a fully specified list of targets from a
// list of prereqs.
type DirectRule struct {
	baseRule
	targets []string
}

func NewDirectRule(targets []string, prereqs []prereq, recipe []string, attrs AttrSet) DirectRule {
	return DirectRule{
		baseRule: baseRule{
			recipe:  recipe,
			prereqs: prereqs,
			attrs:   attrs,
		},
		targets: targets,
	}
}

func NewDirectRuleBase(targets []string, prereqs []string, recipe []string, attrs AttrSet) DirectRule {
	p := make([]prereq, 0, len(prereqs))
	for _, s := range prereqs {
		p = append(p, prereq{name: s})
	}
	return NewDirectRule(targets, p, recipe, attrs)
}

func equal[T comparable](a, b []T) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func (r *DirectRule) Equals(other *DirectRule) bool {
	return r.attrs == other.attrs &&
		equal(r.prereqs, other.prereqs) &&
		equal(r.recipe, other.recipe)
}

func (r *DirectRule) String() string {
	return fmt.Sprintf("%s: %s", strings.Join(r.targets, " "), r.prereqsString())
}

// A MetaRule specifies the targets to build based on a pattern.
type MetaRule struct {
	baseRule
	targets []Pattern
	nomatch map[string]bool
}

// Match returns the submatch and pattern used to perform the match, if there
// is one.
func (r *MetaRule) Match(target string) ([]int, *Pattern) {
	if r.nomatch[target] {
		return nil, nil
	}
	for i, t := range r.targets {
		if s := t.Regex.FindStringSubmatchIndex(target); s != nil {
			return s, &r.targets[i]
		}
	}
	r.nomatch[target] = true
	return nil, nil
}

func (r *MetaRule) String() string {
	targets := make([]string, len(r.targets))
	for i, p := range r.targets {
		targets[i] = p.Regex.String()
	}
	return fmt.Sprintf("%s: %s", strings.Join(targets, " "), r.prereqsString())
}

type AttrSet struct {
	Regex    bool   // regular expression meta-rule
	Virtual  bool   // targets are not files
	Quiet    bool   // is not displayed as part of the build process
	NoMeta   bool   // cannot be matched by meta rules
	NonStop  bool   // does not stop if the recipe fails
	Rebuild  bool   // this rule is always out-of-date
	Linked   bool   // only run this rule if a sub-rule that requires it needs to run
	Implicit bool   // not listed in $input
	Dep      string // dependency file
	Order    bool
}

func (a *AttrSet) UpdateFrom(other AttrSet) {
	a.Regex = a.Regex || other.Regex
	a.Virtual = a.Virtual || other.Virtual
	a.Quiet = a.Quiet || other.Quiet
	a.NoMeta = a.NoMeta || other.NoMeta
	a.NonStop = a.NonStop || other.NonStop
	a.Rebuild = a.Rebuild || other.Rebuild
	a.Linked = a.Linked || other.Linked
	a.Order = a.Order || other.Order
	a.Implicit = a.Implicit || other.Implicit
}

type Pattern struct {
	Suffix bool
	Regex  *regexp.Regexp
}

type RuleSet struct {
	dir         string
	metaRules   []MetaRule
	directRules []DirectRule
	// maps target names into directRules
	// a target may have multiple rules implementing it
	// a rule may have multiple targets pointing to it
	targets map[string][]int
}

type prereq struct {
	name  string
	attrs AttrSet
}

func (p *prereq) addAttrs(attrs AttrSet) {
	p.attrs.UpdateFrom(attrs)
	p.attrs.Dep = attrs.Dep
}

func NewRuleSet(dir string) *RuleSet {
	return &RuleSet{
		metaRules:   make([]MetaRule, 0),
		directRules: make([]DirectRule, 0),
		targets:     make(map[string][]int),
		dir:         dir,
	}
}

func (rs *RuleSet) Add(r Rule) {
	switch r := r.(type) {
	case DirectRule:
		rs.directRules = append(rs.directRules, r)
		k := len(rs.directRules) - 1
		for _, t := range r.targets {
			t = pathJoin(r.dir, t)
			rs.targets[t] = append(rs.targets[t], k)
		}
	case MetaRule:
		rs.metaRules = append(rs.metaRules, r)
	}
}

func (rs *RuleSet) MainTarget() string {
	if len(rs.directRules) == 0 || len(rs.directRules[0].targets) == 0 {
		return ""
	}
	return rs.directRules[0].targets[0]
}

func (rs *RuleSet) AllTargets() []string {
	targets := make([]string, 0, len(rs.targets))
	for k := range rs.targets {
		targets = append(targets, k)
	}
	return targets
}

type attrError struct {
	found rune
}

func (err attrError) Error() string {
	return fmt.Sprintf("unrecognized attribute: %c", err.found)
}

func ParseAttribs(input string) (AttrSet, error) {
	var attrs AttrSet
	r := strings.NewReader(input)
	for r.Len() > 0 {
		c, _, _ := r.ReadRune()
		switch c {
		case 'Q':
			attrs.Quiet = true
		case 'R':
			attrs.Regex = true
		case 'V':
			attrs.Virtual = true
		case 'M':
			attrs.NoMeta = true
		case 'E':
			attrs.NonStop = true
		case 'B':
			attrs.Rebuild = true
		case 'L':
			attrs.Linked = true
		case 'O':
			attrs.Order = true
		case 'I':
			attrs.Implicit = true
		case 'D':
			if r.Len() == 0 {
				return attrs, fmt.Errorf("attribute: no contents found after D")
			}
			c, _, _ = r.ReadRune()
			dep := &bytes.Buffer{}
			if c == '[' {
				found := false
				for r.Len() > 0 {
					c, _, _ = r.ReadRune()
					if c == ']' {
						found = true
						break
					}
					dep.WriteRune(c)
				}
				if !found {
					return attrs, fmt.Errorf("attribute: no ']' found after D")
				}
			} else {
				return attrs, fmt.Errorf("attribute: no '[' found after D")
			}
			attrs.Dep = dep.String()
		default:
			return attrs, attrError{c}
		}
	}

	return attrs, nil
}

func MergeRuleSets(first *RuleSet, rsets []*RuleSet) *RuleSet {
	rs := NewRuleSet(".")

	add := func(r *RuleSet) {
		for _, mr := range r.metaRules {
			rs.Add(mr)
		}
		for _, dr := range r.directRules {
			rs.Add(dr)
		}
	}

	add(first)

	for _, r := range rsets {
		add(r)
	}

	return rs
}
