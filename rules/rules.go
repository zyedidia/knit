package rules

import (
	"fmt"
	"regexp"
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

func (r *MetaRule) Match(target string) ([]int, *Pattern) {
	for i, t := range r.targets {
		if s := t.Rgx.FindStringSubmatchIndex(target); s != nil {
			return s, &r.targets[i]
		}
	}
	return nil, nil
}

type AttrSet struct {
	Regex   bool
	Virtual bool
	Quiet   bool
	NoMeta  bool // rule cannot be matched by meta rules
}

type Pattern struct {
	Suffix bool
	Rgx    *regexp.Regexp
}

type RuleSet struct {
	metaRules   []MetaRule
	directRules []DirectRule
	// maps target names into directRules
	// a target may have multiple rules implementing it
	// a rule may have multiple targets pointing to it
	targets map[string][]int
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

func (rs *RuleSet) MainTargets() []string {
	if len(rs.directRules) == 0 {
		return nil
	}
	return rs.directRules[0].targets
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
		default:
			return attrs, attrError{c}
		}

		pos += w
	}

	return attrs, nil
}
