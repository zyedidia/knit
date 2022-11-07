# Knit 🧶

![Test Workflow](https://github.com/zyedidia/knit/actions/workflows/test.yaml/badge.svg)
[![Go Reference](https://pkg.go.dev/badge/github.com/zyedidia/knit.svg)](https://pkg.go.dev/github.com/zyedidia/knit)
[![Go Report Card](https://goreportcard.com/badge/github.com/zyedidia/knit)](https://goreportcard.com/report/github.com/zyedidia/knit)
[![MIT License](https://img.shields.io/badge/license-MIT-blue.svg)](https://github.com/zyedidia/knit/blob/master/LICENSE)

Knit is a build tool inspired by Make and Plan9 mk. You define rules with a
Make-like syntax within a Lua program. Rules can be passed around as Lua
objects, and you can use the Lua module system to make reusable modules for
building any kind of source code.

Knit also tracks more of your build to give you better incremental builds. For
example, Knit automatically tracks recipes that are executed across builds, so
changing a variable (even at the command-line) can cause a rule to be rebuilt
because Knit can see that the recipe depends on the variable.

Knit is in-progress -- backwards-incompatible changes will be made until a
version 1.0 is released.

# Features

* Knit uses Lua for customization. This makes it possible to write reusable
  build libraries.
* Knit has direct support for sub-builds (compared to make, which requires you
  to spawn a make sub-process to perform a sub-build).
* Knit can automatically convert your build to a Ninja file, a Makefile, or a
  Shell script.
* Knit can hash files to determine if they are out-of-date, rather than just
  relying on file modification times.
* Knit tracks recipe changes, so if you update a variable (in the Knitfile or
  at the command-line), any dependent rules will be automatically rebuilt.
* Knit can automatically clean all files generated during a build.
* Knit can export a compile commands database for use with a language server.
* Knit supports `%` meta-rules and regular expression meta-rules.
* Knit uses rule attributes instead of using targets such as `.SECONDARY` to
  indicate special processing.
* Knit supports virtual attributes that are independent of the file system.
* Knit uses sane variable names like `$input`, `$output`, and `$match` instead
  of Make's `$^`, `$@`, and `$*`.
* Knit builds using all cores by default.
* Knit can generate a build graph visualization using graphviz (dot).

# In-progress features

* Ninja to Knit converter (for compatibility with cmake, and for benchmarking).
  See [knitja](https://github.com/zyedidia/knitja) for the converter tool.
* Input attributes for order-only inputs, C dep files, and possibly more.
* Performance optimizations (including build graph serialization).

# Planned possible features

Some major features are planned, but haven't been implemented yet (and may
never be implemented).

* Automatic dependency tracking using ptrace (Linux-only feature).
* Global build file cache.
* Automatic build sandboxing and sealing.

# Installation

Prebuilt binary:

```
eget zyedidia/knit --pre-release
```

From source:

```
go install github.com/zyedidia/knit/cmd/knit@latest
```

# Example Knitfile

Here is an example Knitfile used for building a simple C project.

```lua
knit = import("knit")

cc = cli.cc or "gcc"
debug = tobool(cli.debug) or false

cflags := -Wall

if debug then
    cflags := $cflags -Og -g
else
    cflags := $cflags -O2
end

src = knit.glob("*.c")
obj = knit.extrepl(src, ".c", ".o")
prog := hello

return b{
    $ build:V: $prog

    $ $prog: $obj
        $cc $cflags $input -o $output

    $ %.o: %.c
        $cc $cflags -c $input -o $output

    $ clean:VBQ:
        knit :all -t clean
}
```

See the [docs](./docs/knit.md) for more information.

See this repository's Knitfile and the tests for more examples.

# Usage

```
Usage of knit:
  knit [TARGETS] [ARGS]

Options:
  -B, --always-build       unconditionally build all targets
      --cache string       directory for caching internal build information (default ".")
  -C, --directory string   run command from directory
  -n, --dry-run            print commands without actually executing
  -f, --file string        knitfile to use (default "knitfile")
      --hash               hash files to determine if they are out-of-date (default true)
  -h, --help               show this help message
  -q, --quiet              don't print commands
  -s, --style string       printer style to use (basic, steps, progress) (default "basic")
  -j, --threads int        number of cores to use (default 8)
  -t, --tool string        subtool to invoke (use '-t list' to list subtools); further flags are passed to the subtool
  -u, --updated strings    treat files as updated
  -v, --version            show version information
```
