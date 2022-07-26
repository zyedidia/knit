package main

import (
	"regexp"
)

type Rule struct {
	Targets []Pattern
	Prereqs []string
	Attrs   AttrSet
	Recipe  []Command
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

type Command struct {
	Name string
	Args []string
}

type AttrSet struct {
	Virtual bool
	Quiet   bool
}

type Pattern struct {
	str string
	rgx *regexp.Regexp
}

func (p *Pattern) Match(s string) bool {
	if p.rgx == nil {
		return p.str == s
	}
	return p.rgx.MatchString(s)
}

type RuleSet struct {
	Rules []Rule
	// maps target names into Rules
	// a target may have multiple rules implementing it
	// a rule may have multiple targets pointing to it
	Targets map[string][]int
}

func newRuleSet(rules ...Rule) *RuleSet {
	rs := &RuleSet{
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
