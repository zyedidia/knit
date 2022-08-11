# Knit 🧶

Knit is a build tool inspired by Make and Plan9 mk. You define rules with a
Make-like syntax within a Lua program. Knit also tracks more of your build to
give you better incremental builds. For example, Knit automatically tracks
recipes that are executed across builds, so changing a variable (even at the
command-line) can cause a rule to be rebuilt because Knit can see that the
recipe depends on the variable.

# Improvements over Make

* Make requires tab characters for indentation, Knit does not.
* Make uses special targets such as `.SECONDARY` to indicate special
  processing. Knit uses rule attributes.
* Knit supports virtual attributes that are independent of the file system.
* Knit uses sane variable names like `$input`, `$output`, and `$match` instead
  of `$^`, `$@`, and `$*`.
* Knit tracks recipe changes, so if you update a variable (in the Knitfile or
  at the command-line), any dependent rules will be automatically rebuilt.
* Knit uses Lua for customization, rather than the Make custom language.
* Make supports `%` meta-rules. Knit supports `%` meta-rules and regular
  expression meta-rules.
* Knit builds using all cores by default.
* Knit's implementation is small and can be easily built for many systems.

# Installation

```
go install github.com/zyedidia/knit@latest
```

# Example Knitfile

Here is an example Knitfile used for building a simple C project.

```tcl
knit = import("knit")

cc = cli.cc or "gcc"
debug = tobool(cli.debug) or false

cflags = "-Wall"

if debug then
    cflags = f"$cflags -Og -g -fsanitize=address"
else
    cflags = f"$cflags -O2"
end

src = knit.glob("*.c")
obj = knit.extrepl(src, ".c", ".o")
prog = "hello"

$ $prog: $obj
    $cc $cflags $input -o $output

$ %.o: %.c
    $cc $cflags -c $input -o $output

$ clean:V:
    rm -f $obj $prog
```

See this repository's Knitfile and the tests for more examples.
