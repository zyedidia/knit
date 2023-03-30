package rules

import (
	"bufio"
	"bytes"
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/zyedidia/knit/expand"
)

type parser struct {
	l        *lexer // underlying lexer
	file     string
	tokenbuf []token  // tokens consumed on the current statement
	rules    *RuleSet // current ruleSet

	errexpand expand.Resolver

	fatal  bool
	errors MultiError
}

type MultiError []error

func (e MultiError) Error() string {
	buf := &bytes.Buffer{}
	for _, err := range e {
		buf.WriteString(err.Error())
		buf.WriteByte('\n')
	}
	return strings.TrimSpace(buf.String())
}

func (p *parser) parseError(context string, expected string, found token) {
	err := fmt.Errorf("%s:%d: syntax error: while %s, expected %s but found %s", p.file, found.line, context, expected, found.String())
	p.errors = append(p.errors, err)
	p.fatal = true
}

func (p *parser) basicErrorAtToken(what string, found token) {
	p.basicErrorAtLine(what, found.line)
}

func (p *parser) basicErrorAtLine(what string, line int) {
	err := fmt.Errorf("%s:%d: syntax error: %s", p.file, line, what)
	p.errors = append(p.errors, err)
}

func (p *parser) expandErrorAtLine(what string, line int) {
	err := fmt.Errorf("%s:%d: expand error: %s", p.file, line, what)
	p.errors = append(p.errors, err)
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

// ParseInto parses 'input' into the rules RuleSet, from the file at 'file'.
func ParseInto(input string, rules *RuleSet, file string, line int) error {
	input = strings.TrimSpace(input)
	l := lex(input, line)
	p := &parser{
		l:        l,
		file:     file,
		tokenbuf: []token{},
		rules:    rules,
		// This function is used to "expand" targets and prereqs. All
		// expansions in targets/prereqs must have been resolved during Lua
		// evaluation, so they are now all errors. The errors have to be thrown
		// during rule parsing rather than Lua evaluation, because the rule is
		// a black-box to Lua, and it doesn't know if an expansion is within a
		// recipe (still legal after Lua evaluation) or within targets/prereqs
		// (illegal after Lua evaluation).
		errexpand: func(name string) (string, error) {
			return "", fmt.Errorf("'%s' does not exist", name)
		},
	}
	state := parseTopLevel
	for t := l.nextToken(); t.typ != tokenEnd; t = l.nextToken() {
		if t.typ == tokenError {
			p.basicErrorAtLine(l.errmsg, t.line)
			break
		}

		state = state(p, t)

		if p.fatal {
			return p.errors
		}
	}

	// insert two dummy newlines to allow parsing of any prereqs or recipeless
	// rules to finish.
	state = state(p, token{tokenNewline, "\n", l.line, l.col})
	state(p, token{tokenNewline, "\n", l.line, l.col})

	if len(p.errors) != 0 {
		return p.errors
	}

	return nil
}

func parseTopLevel(p *parser, t token) parserStateFun {
	switch t.typ {
	case tokenNewline:
		return parseTopLevel
	case tokenWord:
		return parseTargets(p, t)
	default:
		p.parseError("parsing rules",
			"a rule", t)
	}

	return parseTopLevel
}

// Everything up to ':' must be a target.
func parseTargets(p *parser, t token) parserStateFun {
	switch t.typ {
	case tokenWord:
		s, err := expand.Expand(t.val, p.errexpand, p.errexpand, true)
		if err != nil {
			p.expandErrorAtLine(err.Error(), t.line)
		}
		t.val = s
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
	case tokenNewline, tokenGt:
		return parseRecipe
	case tokenColon:
		p.push(t)
		return parsePrereqs
	case tokenWord:
		s, err := expand.Expand(t.val, p.errexpand, p.errexpand, true)
		if err != nil {
			p.expandErrorAtLine(err.Error(), t.line)
		}
		t.val = s
		p.push(t)
	case tokenLSquare, tokenRSquare:
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
		s, err := expand.Expand(t.val, p.errexpand, p.errexpand, true)
		if err != nil {
			p.basicErrorAtToken(err.Error(), t)
		}
		t.val = s
		p.push(t)
	case tokenLSquare, tokenRSquare:
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

	base.dir = p.rules.dir

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
				direct = append(direct, filepath.Clean(str))
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
					Regex: rpat,
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
						msg := fmt.Sprintf("error compiling suffix rule - this is a bug - error: %s", err)
						p.basicErrorAtToken(msg, p.tokenbuf[k])
					}
					patterns = append(patterns, Pattern{
						Regex:  rpat,
						Suffix: true,
					})
				}
			}
		}
	}

	// prereqs
	base.prereqs = make([]prereq, 0)

	var getPrereq func(k int) ([]prereq, int)
	getPrereq = func(k int) ([]prereq, int) {
		if k >= len(p.tokenbuf) {
			return nil, k
		}
		prereqs := make([]prereq, 0)
		t := p.tokenbuf[k]
		k++
		switch t.typ {
		case tokenLSquare:
			for k < len(p.tokenbuf) && p.tokenbuf[k].typ != tokenRSquare {
				p, n := getPrereq(k)
				prereqs = append(prereqs, p...)
				k = n
			}
			k++
		case tokenWord:
			prereqs = append(prereqs, prereq{name: filepath.Clean(t.val)})
		}
		if k >= len(p.tokenbuf) {
			return prereqs, k
		}
		t = p.tokenbuf[k]
		if t.typ != tokenLSquare {
			return prereqs, k
		}

		attribs := &bytes.Buffer{}
		k++
		start := k
		// TODO: doesn't handle balanced square brackets
		for ; k < len(p.tokenbuf); k++ {
			if p.tokenbuf[k].typ == tokenLSquare {
				p.basicErrorAtToken("found '[' inside attribute list", p.tokenbuf[k])
				break
			} else if p.tokenbuf[k].typ == tokenRSquare {
				break
			}
			attribs.WriteString(p.tokenbuf[k].val)
		}
		if k >= len(p.tokenbuf) {
			p.basicErrorAtToken("no ']' found after attribute list", p.tokenbuf[start-1])
		}
		attrs, err := ParseAttribs(attribs.String())
		if err != nil {
			p.basicErrorAtToken(fmt.Sprintf("%v", err), p.tokenbuf[start])
		}
		for i := range prereqs {
			prereqs[i].addAttrs(attrs)
		}
		return prereqs, k
	}
	var preq []prereq
	var k int
	for preq, k = getPrereq(j + 1); k < len(p.tokenbuf); preq, k = getPrereq(k) {
		base.prereqs = append(base.prereqs, preq...)
	}
	base.prereqs = append(base.prereqs, preq...)

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
	commands := make([]string, 0)
	rd := strings.NewReader(recipe)
	wr := &bytes.Buffer{}
	var escape bool

	for rd.Len() > 0 {
		r, _, _ := rd.ReadRune()
		if !escape && r == '\n' {
			commands = append(commands, wr.String())
			wr.Reset()
			escape = false
			continue
		}
		escape = r == '\\'
		wr.WriteRune(r)
	}
	if wr.Len() > 0 {
		commands = append(commands, wr.String())
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
