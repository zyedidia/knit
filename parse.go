package main

import (
	"fmt"
	"regexp"
	"strings"
)

type parser struct {
	l        *lexer   // underlying lexer
	name     string   // name of the file being parsed
	path     string   // full path of the file being parsed
	tokenbuf []token  // tokens consumed on the current statement
	rules    *RuleSet // current ruleSet

	PrintErr func(string)
	Err      func(string)
}

type ErrFns struct {
	PrintErr func(string)
	Err      func(string)
}

// Pretty errors.
func (p *parser) parseError(context string, expected string, found token) {
	p.PrintErr(fmt.Sprintf("%s:%d: syntax error: ", p.name, found.line))
	p.PrintErr(fmt.Sprintf("while %s, expected %s but found '%s'.\n",
		context, expected, found.String()))
	p.Err("")
}

// More basic errors.
func (p *parser) basicErrorAtToken(what string, found token) {
	p.basicErrorAtLine(what, found.line)
}

func (p *parser) basicErrorAtLine(what string, line int) {
	p.Err(fmt.Sprintf("%s:%d: syntax error: %s\n", p.name, line, what))
}

// Accept a token for use in the current statement being parsed.
func (p *parser) push(t token) {
	p.tokenbuf = append(p.tokenbuf, t)
}

// Clear all the accepted tokens. Called when a statement is finished.
func (p *parser) clear() {
	p.tokenbuf = p.tokenbuf[:0]
}

// A parser state function takes a parser and the next token and returns a new
// state function, or nil if there was a parse error.
type parserStateFun func(*parser, token) parserStateFun

// Parse a mkfile, returning a new ruleSet.
func parse(input string, name string, path string, env map[string][]string, errfns ErrFns) *RuleSet {
	rules := &RuleSet{
		Rules:   make([]Rule, 0),
		Targets: make(map[string][]int),
	}
	parseInto(input, name, rules, path, errfns)
	return rules
}

// Parse a mkfile inserting rules into a given ruleSet.
func parseInto(input string, name string, rules *RuleSet, path string, errfns ErrFns) {
	l := lex(input)
	p := &parser{
		l:        l,
		name:     name,
		path:     path,
		tokenbuf: []token{},
		rules:    rules,
		PrintErr: errfns.PrintErr,
		Err:      errfns.Err,
	}
	state := parseTopLevel
	for t := l.nextToken(); t.typ != tokenEnd; t = l.nextToken() {
		if t.typ == tokenError {
			p.basicErrorAtLine(l.errmsg, t.line)
			break
		}

		state = state(p, t)
	}

	// insert a dummy newline to allow parsing of any assignments or recipeless
	// rules to finish.
	state = state(p, token{tokenNewline, "\n", l.line, l.col})

	// TODO: Error when state != parseTopLevel
}

// We are at the top level of a mkfile, expecting rules, assignments, or
// includes.
func parseTopLevel(p *parser, t token) parserStateFun {
	switch t.typ {
	case tokenNewline:
		return parseTopLevel
	case tokenWord:
		return parseTarget(p, t)
	default:
		p.parseError("parsing mkfile",
			"a rule", t)
	}

	return parseTopLevel
}

// Consumed one bare string ot the beginning of the line.
func parseTarget(p *parser, t token) parserStateFun {
	p.push(t)
	switch t.typ {
	case tokenWord:
		p.push(t)
		return parseTargets

	case tokenColon:
		p.push(t)
		return parseAttributesOrPrereqs

	default:
		p.parseError("reading a target",
			"'=', ':', or another target", t)
	}

	return parseTopLevel // unreachable
}

// Everything up to ':' must be a target.
func parseTargets(p *parser, t token) parserStateFun {
	switch t.typ {
	case tokenWord:
		p.push(t)
	case tokenColon:
		p.push(t)
		return parseAttributesOrPrereqs

	default:
		p.parseError("reading a rule's targets",
			"filename or pattern", t)
	}

	return parseTargets
}

// Consume one or more strings followed by a first ':'.
func parseAttributesOrPrereqs(p *parser, t token) parserStateFun {
	switch t.typ {
	case tokenNewline:
		return parseRecipe
	case tokenColon:
		p.push(t)
		return parsePrereqs
	case tokenWord:
		p.push(t)
	default:
		p.parseError("reading a rule's attributes or prerequisites",
			"an attribute, pattern, or filename", t)
	}

	return parseAttributesOrPrereqs
}

// Targets and attributes and the second ':' have been consumed.
func parsePrereqs(p *parser, t token) parserStateFun {
	switch t.typ {
	case tokenNewline:
		return parseRecipe
	case tokenWord:
		p.push(t)

	default:
		p.parseError("reading a rule's prerequisites",
			"filename or pattern", t)
	}

	return parsePrereqs
}

// An entire rule has been consumed.
func parseRecipe(p *parser, t token) parserStateFun {
	// Assemble the rule!
	r := Rule{}

	// find one or two colons
	i := 0
	for ; i < len(p.tokenbuf) && p.tokenbuf[i].typ != tokenColon; i++ {
	}
	j := i + 1
	for ; j < len(p.tokenbuf) && p.tokenbuf[j].typ != tokenColon; j++ {
	}

	// rule has attributes
	if j < len(p.tokenbuf) {
		attribs := make([]string, 0)
		for k := i + 1; k < j; k++ {
			attribs = append(attribs, p.tokenbuf[k].val)
		}
		err := r.parseAttribs(attribs)
		if err != nil {
			msg := fmt.Sprintf("while reading a rule's attributes expected an attribute but found \"%c\".", err.found)
			p.basicErrorAtToken(msg, p.tokenbuf[i+1])
		}

		if r.Attrs.Regex {
			r.Meta = true
		}
	} else {
		j = i
	}

	// targets
	r.Targets = make([]Pattern, 0)
	for k := 0; k < i; k++ {
		targetstr := p.tokenbuf[k].val
		r.Targets = append(r.Targets, Pattern{str: targetstr})

		if r.Attrs.Regex {
			rpat, err := regexp.Compile("^" + targetstr + "$")
			if err != nil {
				msg := fmt.Sprintf("invalid regular expression: %q", err)
				p.basicErrorAtToken(msg, p.tokenbuf[k])
			}
			r.Targets[len(r.Targets)-1].rgx = rpat
		} else {
			idx := strings.IndexRune(targetstr, '%')
			if idx >= 0 {
				var left, right string
				if idx > 0 {
					left = regexp.QuoteMeta(targetstr[:idx])
				}
				if idx < len(targetstr)-1 {
					right = regexp.QuoteMeta(targetstr[idx+1:])
				}

				patstr := fmt.Sprintf("^%s(.*)%s$", left, right)
				rpat, err := regexp.Compile(patstr)
				if err != nil {
					msg := fmt.Sprintf("error compiling suffix rule. This is a bug. Error: %s", err)
					p.basicErrorAtToken(msg, p.tokenbuf[k])
				}
				r.Targets[len(r.Targets)-1].rgx = rpat
				r.Targets[len(r.Targets)-1].suffix = true
				r.Meta = true
			}
		}
	}

	// prereqs
	r.Prereqs = make([]string, 0)
	for k := j + 1; k < len(p.tokenbuf); k++ {
		r.Prereqs = append(r.Prereqs, p.tokenbuf[k].val)
	}

	if t.typ == tokenRecipe {
		r.Recipe = parseCommands(stripIndentation(t.val, t.col))
	}

	p.rules.add(r)
	p.clear()

	// the current token doesn't belong to this rule
	if t.typ != tokenRecipe {
		return parseTopLevel(p, t)
	}

	return parseTopLevel
}

func parseCommands(recipe string) []Command {
	// TODO:
	parts := strings.Split(recipe, "\n")
	commands := make([]Command, 0, len(parts))
	for _, p := range parts {
		if len(strings.TrimSpace(p)) == 0 {
			continue
		}
		commands = append(commands, Command{
			Name: "sh",
			Args: []string{"-c", p},
		})
	}
	return commands
}
