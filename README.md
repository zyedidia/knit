# Knit 🧶

![Test Workflow](https://github.com/zyedidia/knit/actions/workflows/test.yaml/badge.svg)
[![Go Reference](https://pkg.go.dev/badge/github.com/zyedidia/knit.svg)](https://pkg.go.dev/github.com/zyedidia/knit)
[![Go Report Card](https://goreportcard.com/badge/github.com/zyedidia/knit)](https://goreportcard.com/report/github.com/zyedidia/knit)
[![MIT License](https://img.shields.io/badge/license-MIT-blue.svg)](https://github.com/zyedidia/knit/blob/master/LICENSE)

Knit is a build tool inspired by Make and Plan9 Mk. You define rules with a
Make-like syntax within a Lua program. Rules can be passed around as Lua
objects, and you can use the Lua module system to make reusable modules for
building any kind of source code.

Knit tracks more of your build to give you better incremental builds. For
example, Knit automatically adds an implicit dependency on a rule's recipe, so
if you change a recipe (either directly or through a variable change), Knit
will automatically re-run all rules that were affected.

Knit has support for namespaced sub-builds that execute relative to their
directory, but avoids the build fragmentation caused by forcing you to spawn
build sub-processes. No more `make -C` to do sub-builds! Everything is tracked
by the root Knitfile, but you can still make directory-specific rules.

Knit's rules language is heavily inspired by [Plan9
Mk](https://9p.io/sys/doc/mk.html). In some ways, Knit can be considered a
modern re-implementation of Mk (with some differences), and a Lua
meta-programming system built on top of it.

Knit is in-progress -- backwards-incompatible changes will be made until a
version 1.0 is released.

# Features

* Knit uses Lua for customization. This makes it possible to write reusable
  build libraries, and in general makes it easier to write powerful and
  expressive builds.
* Knit has built-in syntax for a rules language inspired by Make and Plan9 Mk.
  This makes it very familiar to anyone who has used Make/Mk.
* Knit has direct support for sub-builds (compared to Make, which requires you
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
* Input attributes:
    * [x] Order only inputs.
    * [ ] Header dependency files (`.d` files).
    * [ ] Ptrace enabled automatic dependency discovery.
* Performance optimizations (build graph serialization).

# Planned possible features

Some major features are planned, but haven't been implemented yet (and may
never be implemented).

* Automatic dependency tracking using ptrace (Linux-only feature).
* Global build file cache (similar to `ccache`, but for every command that is
  executed).
* Automatic build sandboxing.

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
local knit = require("knit")

local conf = {
    cc = cli.cc or "gcc",
    debug = tobool(cli.debug) or false,
}

local cflags := -Wall

if conf.debug then
    cflags := $cflags -Og -g
else
    cflags := $cflags -O2
end

local src = knit.glob("*.c")
local obj = knit.extrepl(src, ".c", ".o")
local prog := hello

return b{
    $ $prog: $obj
        $(conf.cc) $cflags $input -o $output
    $ %.o: %.c
        $(conf.cc) $cflags -c $input -o $output
    $ clean:VBQ:
        knit :all -t clean
}
```

Running `knit hello` would build all the necessary `.o` files and then link
them together. Running `knit clean` will run `knit :all -t clean`, a sub-tool
that automatically cleans outputs generated by the special rule `:all` that
builds all targets (i.e., automatically cleaning all build outputs). The `VBQ`
attributes on the clean rule means that it is virtual (not referring to a file
on the system), should always be built (out-of-date), and quiet (does not print
the command it executes).

Note that Knitfiles are Lua programs with some modified syntax: special syntax
using `$` for defining rules, and special syntax using `:=` for defining raw
strings (no quotes) with interpolation.

See the [docs](./docs/knit.md) for more information.

See [examples](./examples) for a few examples, and see this repository's
Knitfile and the tests for even more examples.

# Feedback

Knit is at an early stage in development and at a point where it would be
useful to get feedback from others to improve it. If you have feedback, or
questions about how to use it, please open a discussion. It would be great to
discuss the good and bad parts of the current design, and how it can be
improved.

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

# Contributing

If you find a bug or have a feature request please open an issue for
discussion. I am sometimes prone to being unresponsive to pull requests, so I
apologize in advance. Please ping me if I forget to respond. If you have a
feature you would like to implement, please double check with me about the
feature before investing lots of time into implementing it.

If you have a question or feedback about the current design, please open a
discussion.
