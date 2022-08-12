package rules

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/zyedidia/knit/expand"
)

type parser struct {
	l        *lexer   // underlying lexer
	path     string   // full path of the file being parsed
	tokenbuf []token  // tokens consumed on the current statement
	rules    *RuleSet // current ruleSet

	expands ExpandFns

	printErr func(string)
	errFn    func(string)
}

type ExpandFns struct {
	Rvar  func(string) (string, error)
	Rexpr func(string) (string, error)
}

type ErrFns struct {
	PrintErr func(string)
	Err      func(string)
}

// Pretty errors.
func (p *parser) parseError(context string, expected string, found token) {
	p.printErr(fmt.Sprintf("%s:%d: syntax error: ", p.path, found.line))
	p.printErr(fmt.Sprintf("while %s, expected %s but found %s.\n",
		context, expected, found.String()))
	p.errFn("parse error")
}

// More basic errors.
func (p *parser) basicErrorAtToken(what string, found token) {
	p.basicErrorAtLine(what, found.line)
}

func (p *parser) basicErrorAtLine(what string, line int) {
	p.errFn(fmt.Sprintf("%s:%d: syntax error: %s\n", p.path, line, what))
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

func expandInput(s string, expands ExpandFns) (string, error) {
	b := bufio.NewReader(strings.NewReader(s))
	buf := &bytes.Buffer{}
	remaining := len(s)
	for remaining > 0 {
		l, err := b.ReadString('\n')
		if err != nil && err != io.EOF {
			return "", err
		}
		remaining -= len(l)
		r, size := utf8.DecodeRuneInString(l)
		if size == 0 || strings.ContainsRune(" \t\r\n", r) {
			buf.WriteString(l)
			continue
		}
		e, err := expand.Expand(l, expands.Rvar, expands.Rexpr)
		if err != nil {
			return "", err
		}
		buf.WriteString(e)
	}
	return buf.String(), nil
}

func ParseInto(input string, rules *RuleSet, path string, errfns ErrFns, expands ExpandFns) (err error) {
	input = strings.Replace(input, "\\\n", "", -1)
	input = stripIndentation(input, 2)
	input, err = expandInput(input, expands)
	if err != nil {
		return fmt.Errorf("expand: %w", err)
	}
	l := lex(input)
	p := &parser{
		l:        l,
		path:     path,
		tokenbuf: []token{},
		rules:    rules,
		printErr: errfns.PrintErr,
		errFn:    errfns.Err,
		expands:  expands,
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
	state(p, token{tokenNewline, "\n", l.line, l.col})

	return nil

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
	var base baseRule
	var r Rule
	var meta bool

	// find one or two colons
	i := 0
	for ; i < len(p.tokenbuf) && p.tokenbuf[i].typ != tokenColon; i++ {
	}
	j := i + 1
	for ; j < len(p.tokenbuf) && p.tokenbuf[j].typ != tokenColon; j++ {
	}

	// rule has attributes
	if j < len(p.tokenbuf) {
		attribs := &bytes.Buffer{}
		for k := i + 1; k < j; k++ {
			attribs.WriteString(p.tokenbuf[k].val)
		}
		attrs, err := ParseAttribs(attribs.String())
		if err != nil {
			msg := fmt.Sprintf("%v", err)
			p.basicErrorAtToken(msg, p.tokenbuf[i+1])
		}
		base.attrs = attrs

		if base.attrs.Regex {
			meta = true
		}
	} else {
		j = i
	}

	// targets
	direct := make([]string, 0)
	if !meta {
		for k := 0; k < i; k++ {
			str := p.tokenbuf[k].val
			if strings.ContainsRune(str, '%') {
				meta = true
				break
			} else {
				direct = append(direct, str)
			}
		}
	}

	patterns := make([]Pattern, 0)
	if meta {
		for k := 0; k < i; k++ {
			str := p.tokenbuf[k].val
			if base.attrs.Regex {
				rpat, err := regexp.Compile("^" + str + "$")
				if err != nil {
					msg := fmt.Sprintf("invalid regular expression: %q", err)
					p.basicErrorAtToken(msg, p.tokenbuf[k])
				}
				patterns = append(patterns, Pattern{
					Rgx: rpat,
				})
			} else {
				idx := strings.IndexRune(str, '%')
				if idx >= 0 {
					var left, right string
					if idx > 0 {
						left = regexp.QuoteMeta(str[:idx])
					}
					if idx < len(str)-1 {
						right = regexp.QuoteMeta(str[idx+1:])
					}

					patstr := fmt.Sprintf("^%s(.*)%s$", left, right)
					rpat, err := regexp.Compile(patstr)
					if err != nil {
						msg := fmt.Sprintf("error compiling suffix rule. This is a bug. Error: %s", err)
						p.basicErrorAtToken(msg, p.tokenbuf[k])
					}
					patterns = append(patterns, Pattern{
						Rgx:    rpat,
						Suffix: true,
					})
				}
			}
		}
	}

	// prereqs
	base.prereqs = make([]string, 0)
	for k := j + 1; k < len(p.tokenbuf); k++ {
		base.prereqs = append(base.prereqs, p.tokenbuf[k].val)
	}

	if t.typ == tokenRecipe {
		base.recipe = parseCommands(stripIndentation(t.val, t.col))
	}

	if meta {
		r = MetaRule{
			baseRule: base,
			targets:  patterns,
		}
	} else {
		r = DirectRule{
			baseRule: base,
			targets:  direct,
		}
	}

	p.rules.Add(r)
	p.clear()

	// the current token doesn't belong to this rule
	if t.typ != tokenRecipe {
		return parseTopLevel(p, t)
	}

	return parseTopLevel
}

func parseCommands(recipe string) []string {
	parts := strings.Split(recipe, "\n")
	commands := make([]string, 0, len(parts))
	for _, p := range parts {
		if len(strings.TrimSpace(p)) == 0 {
			continue
		}
		commands = append(commands, p)
	}
	return commands
}

// Try to unindent a recipe, so that it begins at column 0.
func stripIndentation(s string, mincol int) string {
	// trim leading whitespace
	reader := bufio.NewReader(strings.NewReader(s))
	output := ""
	for {
		line, err := reader.ReadString('\n')
		col := 0
		i := 0
		for i < len(line) && col < mincol {
			c, w := utf8.DecodeRuneInString(line[i:])
			if strings.ContainsRune(" \t\n", c) {
				col += 1
				i += w
			} else {
				break
			}
		}
		output += line[i:]

		if err != nil {
			break
		}
	}

	return output
}
