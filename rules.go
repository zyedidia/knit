package mak

import (
	"regexp"
	"unicode/utf8"

	"github.com/zyedidia/gotcl"
)

type rule interface {
}

type baseRule struct {
	prereqs []string
	attrs   attrSet
	recipe  []string
}

type directRule struct {
	baseRule
	targets []string
}

func (r *directRule) Match(target string) bool {
	for _, t := range r.targets {
		if t == target {
			return true
		}
	}
	return false
}

type metaRule struct {
	baseRule
	targets []pattern
}

func (r *metaRule) Match(target string) bool {
	for _, t := range r.targets {
		if t.Match(target) {
			return true
		}
	}
	return false
}

type attrSet struct {
	regex   bool
	virtual bool
	quiet   bool
}

type pattern struct {
	suffix bool
	rgx    *regexp.Regexp
}

func (p *pattern) Match(s string) bool {
	return p.rgx.MatchString(s)
}

type ruleSet struct {
	vars        map[string]*gotcl.TclObj
	metaRules   []metaRule
	directRules []directRule
	// maps target names into directRules
	// a target may have multiple rules implementing it
	// a rule may have multiple targets pointing to it
	targets map[string][]int
}

func newRuleSet() *ruleSet {
	return &ruleSet{
		vars:        make(map[string]*gotcl.TclObj),
		metaRules:   make([]metaRule, 0),
		directRules: make([]directRule, 0),
		targets:     make(map[string][]int),
	}
}

func (rs *ruleSet) add(r rule) {
	switch r := r.(type) {
	case directRule:
		rs.directRules = append(rs.directRules, r)
		k := len(rs.directRules) - 1
		for _, t := range r.targets {
			rs.targets[t] = append(rs.targets[t], k)
		}
	case metaRule:
		rs.metaRules = append(rs.metaRules, r)
	}
}

type attribError struct {
	found rune
}

func (r *baseRule) parseAttribs(inputs []string) *attribError {
	for i := 0; i < len(inputs); i++ {
		input := inputs[i]
		pos := 0
		for pos < len(input) {
			c, w := utf8.DecodeRuneInString(input[pos:])
			switch c {
			case 'Q':
				r.attrs.quiet = true
			case 'R':
				r.attrs.regex = true
			case 'V':
				r.attrs.virtual = true
			default:
				return &attribError{c}
			}

			pos += w
		}
	}

	return nil
}
