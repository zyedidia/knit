
# Rules

## Attributes

# Built-in Lua functions

* `import(pkg string) table`: import a built-in package. Currently the only built-in package is `knit`.

* `rule(rule string)`: define a rule. The `$` syntax is shorthand for this function.

* `tostring(value) string`: convert an arbitrary value to a string.

* `tobool(value)` bool: convert an arbitrary value to a boolean. A nil value
  will return nil, the strings `false`, `off`, or `0` will become false. A
  boolean will not be converted. Anything else will be true.

* `eval(code string): value`: evaluates a Lua expression in the global scope and returns the result.

* `f"..."`, `f(s string) string`: formats a string using `$var` or `$(var)` to
  expand variables. Does not expand expressions.

# The `knit` Lua package

The `knit` package can be imported with `import("knit")`, and provides the following functions:

* `repl(in []string, patstr, repl string) ([]string, error)`: replace all
  occurrences of the Go regular expression `patstr` with `repl` within `in`.

* `extrepl(in []string, ext, repl string) []string`: replace all occurrences of the
  literal string `ext` as a suffix with `repl` within `in`.

* `glob(pat string) []string`: return all files in the current working
  directory that match the glob `pat`.

* `shell(cmd string) string`: execute a command with the shell and return its
  output. If the command exits with an error the returned output will be the
  contents of the error.

* `trim(s string) string`: trim leading and trailing whitespace from a string.

* `abs(path string) (string, error)`: return the absolute path of a path.

# CLI and environment variables

# Flags

# Further customization
