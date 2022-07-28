package main

import (
	"regexp"
	"unicode/utf8"

	"github.com/zyedidia/gotcl"
)

type Rule struct {
	Targets []Pattern
	Prereqs []string
	Attrs   AttrSet
	Recipe  []string
	Meta    bool
}

func (r *Rule) Match(target string) bool {
	for _, t := range r.Targets {
		if t.Match(target) {
			return true
		}
	}
	return false
}

type AttrSet struct {
	Regex   bool
	Virtual bool
	Quiet   bool
}

type Pattern struct {
	suffix bool
	str    string
	rgx    *regexp.Regexp
}

func (p *Pattern) Match(s string) bool {
	if p.rgx == nil {
		return p.str == s
	}
	return p.rgx.MatchString(s)
}

type RuleSet struct {
	Vars  map[string]*gotcl.TclObj
	Rules []Rule
	// maps target names into Rules
	// a target may have multiple rules implementing it
	// a rule may have multiple targets pointing to it
	Targets map[string][]int
}

func newRuleSet(rules ...Rule) *RuleSet {
	rs := &RuleSet{
		Vars:    make(map[string]*gotcl.TclObj),
		Rules:   make([]Rule, 0, len(rules)),
		Targets: make(map[string][]int),
	}
	for _, r := range rules {
		rs.add(r)
	}
	return rs
}

func (rs *RuleSet) add(r Rule) {
	rs.Rules = append(rs.Rules, r)
	k := len(rs.Rules) - 1
	for _, p := range r.Targets {
		if p.rgx == nil {
			rs.Targets[p.str] = append(rs.Targets[p.str], k)
		}
	}
}

// Error parsing an attribute
type attribError struct {
	found rune
}

// Read attributes for an array of strings, updating the rule.
func (r *Rule) parseAttribs(inputs []string) *attribError {
	for i := 0; i < len(inputs); i++ {
		input := inputs[i]
		pos := 0
		for pos < len(input) {
			c, w := utf8.DecodeRuneInString(input[pos:])
			switch c {
			case 'Q':
				r.Attrs.Quiet = true
			case 'R':
				r.Attrs.Regex = true
			case 'V':
				r.Attrs.Virtual = true
			default:
				return &attribError{c}
			}

			pos += w
		}
	}

	return nil
}
