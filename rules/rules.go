package rules

import (
	"fmt"
	"regexp"
	"strings"
	"unicode/utf8"
)

type Rule interface {
	isRule()
}

type baseRule struct {
	prereqs []string
	attrs   AttrSet
	recipe  []string
}

func (b baseRule) isRule() {}

// A DirectRule specifies how to build a fully specified list of targets from a
// list of prereqs.
type DirectRule struct {
	baseRule
	targets []string
}

func NewDirectRule(targets, prereqs, recipe []string, attrs AttrSet) DirectRule {
	return DirectRule{
		baseRule: baseRule{
			recipe:  recipe,
			prereqs: prereqs,
			attrs:   attrs,
		},
		targets: targets,
	}
}

func equal(a, b []string) bool {
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
	return fmt.Sprintf("%s: %s", strings.Join(r.targets, " "), strings.Join(r.prereqs, " "))
}

// A MetaRule specifies the targets to build based on a pattern.
type MetaRule struct {
	baseRule
	targets []Pattern
}

func NewMetaRule(targets []Pattern, prereqs, recipe []string, attrs AttrSet) MetaRule {
	return MetaRule{
		baseRule: baseRule{
			recipe:  recipe,
			prereqs: prereqs,
			attrs:   attrs,
		},
		targets: targets,
	}
}

// Match returns the submatch and pattern used to perform the match, if there
// is one.
func (r *MetaRule) Match(target string) ([]int, *Pattern) {
	for i, t := range r.targets {
		if s := t.Regex.FindStringSubmatchIndex(target); s != nil {
			return s, &r.targets[i]
		}
	}
	return nil, nil
}

func (r *MetaRule) String() string {
	targets := make([]string, len(r.targets))
	for i, p := range r.targets {
		targets[i] = p.Regex.String()
	}
	return fmt.Sprintf("%s: %s", strings.Join(targets, " "), strings.Join(r.prereqs, " "))
}

type AttrSet struct {
	Regex   bool // regular expression meta-rule
	Virtual bool // targets are not files
	Quiet   bool // is not displayed as part of the build process
	NoMeta  bool // cannot be matched by meta rules
	NonStop bool // does not stop if the recipe fails
	Rebuild bool // this rule is always out-of-date
	Linked  bool // only run this rule if a sub-rule that requires it needs to run
}

func (a *AttrSet) UpdateFrom(other AttrSet) {
	a.Regex = a.Regex || other.Regex
	a.Virtual = a.Virtual || other.Virtual
	a.Quiet = a.Quiet || other.Quiet
	a.NoMeta = a.NoMeta || other.NoMeta
	a.NonStop = a.NonStop || other.NonStop
	a.Rebuild = a.Rebuild || other.Rebuild
	a.Linked = a.Linked || other.Linked
}

type Pattern struct {
	Suffix bool
	Regex  *regexp.Regexp
}

type RuleSet struct {
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

func parsePrereq(input string) (p prereq, err error) {
	before, after, found := strings.Cut(input, "[")
	p.name = before
	if found && strings.HasSuffix(after, "]") {
		p.attrs, err = ParseAttribs(after[:len(after)-1])
	}
	return
}

func NewRuleSet() *RuleSet {
	return &RuleSet{
		metaRules:   make([]MetaRule, 0),
		directRules: make([]DirectRule, 0),
		targets:     make(map[string][]int),
	}
}

func (rs *RuleSet) Add(r Rule) {
	switch r := r.(type) {
	case DirectRule:
		rs.directRules = append(rs.directRules, r)
		k := len(rs.directRules) - 1
		for _, t := range r.targets {
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
	pos := 0
	for pos < len(input) {
		c, w := utf8.DecodeRuneInString(input[pos:])
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
		default:
			return attrs, attrError{c}
		}

		pos += w
	}

	return attrs, nil
}
