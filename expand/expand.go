package expand

import (
	"bufio"
	"bytes"
	"io"
	"strings"
)

func varStart(b byte) bool {
	return (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z') || (b == '_')
}

func varInner(b byte) bool {
	return varStart(b) || (b >= '0' && b <= '9')
}

type Resolver func(name string) (value string, err error)

func Expand(s string, rvar Resolver, rexpr Resolver, escape bool) (string, error) {
	return ExpandSpecial(s, rvar, rexpr, '$', escape)
}

func ExpandSpecial(s string, rvar Resolver, rexpr Resolver, special byte, escape bool) (string, error) {
	return expand(bufio.NewReader(strings.NewReader(s)), rvar, rexpr, special, escape)
}

// special is '$' or '%' or some other symbol
func expand(r *bufio.Reader, rvar Resolver, rexpr Resolver, special byte, escape bool) (string, error) {
	buf := &bytes.Buffer{}
	exprbuf := &bytes.Buffer{}
	pos := 0

	braceLevel := 0
	inExpr := false
	inVar := false

	var expandErr error

	for {
		b, err := r.ReadByte()
		if err == io.EOF {
			break
		} else if err != nil {
			return "", err
		}
		pos++

		switch b {
		case special:
			if inExpr {
				break
			}
			p, err := r.Peek(1)
			if err == io.EOF {
				break
			} else if err != nil {
				return "", err
			}
			if p[0] == special {
				r.ReadByte()
				pos++
				buf.WriteByte(special)
				// due to two-stage expansion sometimes it is useful to
				// translate '$$' to '$$' instead of to '$'. This way the
				// escape stays as an escape in the output, to be used in
				// a future expansion
				if !escape {
					buf.WriteByte(special)
				}
				continue
			} else if p[0] == '(' {
				r.ReadByte()
				pos++
				inExpr = true
				braceLevel = 0
				continue
			} else if varStart(p[0]) {
				inVar = true
				continue
			}
		case '(':
			if inExpr {
				braceLevel++
			}
		case ')':
			if inExpr && braceLevel == 0 {
				inExpr = false
				value, err := rexpr(exprbuf.String())
				if err != nil {
					buf.WriteByte(special)
					buf.WriteByte('(')
					buf.WriteString(exprbuf.String())
					buf.WriteByte(')')
					if expandErr == nil {
						expandErr = err
					}
				} else {
					buf.WriteString(value)
				}
				exprbuf.Reset()
				continue
			} else if inExpr {
				braceLevel--
			}
		}
		if inExpr || (inVar && varInner(b)) {
			exprbuf.WriteByte(b)

			if inVar {
				p, err := r.Peek(1)
				if err != nil && err != io.EOF {
					return "", err
				}
				if len(p) == 0 || !varInner(p[0]) {
					inVar = false
					value, err := rvar(exprbuf.String())
					if err != nil {
						buf.WriteByte(special)
						buf.WriteString(exprbuf.String())
						if expandErr == nil {
							expandErr = err
						}
					} else {
						buf.WriteString(value)
					}
					exprbuf.Reset()
				}
			}
		} else if !inExpr && !inVar {
			buf.WriteByte(b)
		}
	}

	return buf.String(), expandErr
}
