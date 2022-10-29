package rules

import (
	"bytes"
	"encoding/gob"
	"fmt"
	"regexp"
	"strings"
	"unicode/utf8"
)

type Rule interface {
	isRule()
}

type BaseRule struct {
	Prereqs []string
	Attrs   AttrSet
	Recipe  []string
}

func (b BaseRule) isRule() {}

// A DirectRule specifies how to build a fully specified list of targets from a
// list of prereqs.
type DirectRule struct {
	BaseRule
	Targets []string
}

func NewDirectRule(targets, prereqs, recipe []string, attrs AttrSet) DirectRule {
	return DirectRule{
		BaseRule: BaseRule{
			Recipe:  recipe,
			Prereqs: prereqs,
			Attrs:   attrs,
		},
		Targets: targets,
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
	return r.Attrs == other.Attrs &&
		equal(r.Prereqs, other.Prereqs) &&
		equal(r.Recipe, other.Recipe)
}

func (r *DirectRule) String() string {
	return fmt.Sprintf("%s: %s", strings.Join(r.Targets, " "), strings.Join(r.Prereqs, " "))
}

// A MetaRule specifies the targets to build based on a pattern.
type MetaRule struct {
	BaseRule
	Targets []Pattern
}

func NewMetaRule(targets []Pattern, prereqs, recipe []string, attrs AttrSet) MetaRule {
	return MetaRule{
		BaseRule: BaseRule{
			Recipe:  recipe,
			Prereqs: prereqs,
			Attrs:   attrs,
		},
		Targets: targets,
	}
}

// Match returns the submatch and pattern used to perform the match, if there
// is one.
func (r *MetaRule) Match(target string) ([]int, *Pattern) {
	for i, t := range r.Targets {
		if s := t.Regex.FindStringSubmatchIndex(target); s != nil {
			return s, &r.Targets[i]
		}
	}
	return nil, nil
}

func (r *MetaRule) String() string {
	targets := make([]string, len(r.Targets))
	for i, p := range r.Targets {
		targets[i] = p.Regex.String()
	}
	return fmt.Sprintf("%s: %s", strings.Join(targets, " "), strings.Join(r.Prereqs, " "))
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

type Pattern struct {
	Suffix bool
	Regex  *regexp.Regexp
}

func (p *Pattern) GobEncode() ([]byte, error) {
	buf := &bytes.Buffer{}
	enc := gob.NewEncoder(buf)
	err := enc.Encode(p.Suffix)
	if err != nil {
		return nil, err
	}
	err = enc.Encode(p.Regex.String())
	return buf.Bytes(), err
}

func (p *Pattern) GobDecode(b []byte) error {
	dec := gob.NewDecoder(bytes.NewReader(b))
	err := dec.Decode(&p.Suffix)
	if err != nil {
		return err
	}
	var rgx string
	err = dec.Decode(&rgx)
	if err != nil {
		return err
	}
	p.Regex, err = regexp.Compile(rgx)
	return err
}

type RuleSet struct {
	MetaRules   []MetaRule
	DirectRules []DirectRule
	// maps target names into directRules
	// a target may have multiple rules implementing it
	// a rule may have multiple Targets pointing to it
	Targets map[string][]int
}

func NewRuleSet() *RuleSet {
	return &RuleSet{
		MetaRules:   make([]MetaRule, 0),
		DirectRules: make([]DirectRule, 0),
		Targets:     make(map[string][]int),
	}
}

func (rs *RuleSet) Add(r Rule) {
	switch r := r.(type) {
	case DirectRule:
		rs.DirectRules = append(rs.DirectRules, r)
		k := len(rs.DirectRules) - 1
		for _, t := range r.Targets {
			rs.Targets[t] = append(rs.Targets[t], k)
		}
	case MetaRule:
		rs.MetaRules = append(rs.MetaRules, r)
	}
}

func (rs *RuleSet) MainTarget() string {
	if len(rs.DirectRules) == 0 || len(rs.DirectRules[0].Targets) == 0 {
		return ""
	}
	return rs.DirectRules[0].Targets[0]
}

func (rs *RuleSet) AllTargets() []string {
	targets := make([]string, 0, len(rs.Targets))
	for k := range rs.Targets {
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
