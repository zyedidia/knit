# Take

A fresh take on Make. Take is a build tool inspired by Make and Plan9 mk. You
define rules in a Make-like syntax, and customize them using Tcl (rather than
Make's custom language). Take also tracks more of your build to ensure optimal
incremental builds. For example, Take automatically tracks recipes that are
executed across builds, so changing a variable can cause a rule to be rebuilt
because Take can see that the recipe depends on the variable.

# Improvements over Make

* Make requires tab characters for indentation, Take does not.
* Make uses special targets such as `.SECONDARY` to indicate special
  processing. Take uses rule attributes.
* Take supports virtual attributes that are independent of the file system.
* Take uses sane variable names like `$in`, `$out`, and `$stem` instead of
  `$^`, `$@`, and `$*`.
* Take tracks recipe changes, so if you update a variable (in the Takefile or
  at the command-line), any dependent rules will be automatically rebuilt.
* Take uses Tcl for customization, rather than the Make custom language.
* Make supports `%` meta-rules. Take supports `%` meta-rules and regular
  expression meta-rules.
* Take builds using all cores by default.
* Take's implementation is small: roughly 1,500 LOC (4,500 including the Tcl
  interpreter). Take is written in Go and can be easily built for many systems.

# Installation

```
go install github.com/zyedidia/take@latest
```

# Example Takefile

Here is an example Takefile used for building a simple C project.

```tcl
set obj [extrepl [glob *.c] .c .o]
set prog hello

setdef cc gcc
setdef debug 0
set cflags -Wall

if { $debug } {
    set cflags "$cflags -Og -g -fsanitize=address,undefined"
} else {
    set cflags "$cflags -O2"
}

rules {

%.o: %.c
    $cc $cflags -c $in -o $out
$prog: $obj
    $cc $ldflags $cflags $in -o $out
clean:V:
    rm -f $obj $prog

}
```

See also this repository's Takefile for an example of regular expression rules.
