---
title: knit
section: 1
header: Knit Manual
---

# NAME
  Knit - Simple and powerful build system

# SYNOPSIS
  knit `[TARGETS] [OPTIONS]`

# DESCRIPTION
  Knit is a build tool inspired by Make and Plan9 mk. You define rules with a
  Make-like syntax within a Lua program. Rules can be passed around as Lua
  objects, and you can use the Lua module system to make reusable modules for
  building any kind of source code.

  Knit also tracks your build to give you better incremental builds. For
  example, Knit automatically tracks recipes that are executed across builds,
  so changing a variable (even at the command-line) can cause a rule to be
  rebuilt because Knit can see that the recipe depends on the variable. Knit
  uses file modification times and file hashes to determine when files have
  been updated.

# TOOLS
  When running **`knit target`**, Knit builds a dependency graph, and by
  default then executes it. If the **`-t`** option is specified, a sub-tool is
  run on the graph instead of performing the build. Sub-tools can be used to
  inspect the graph, convert the rules into another build format (e.g., Make,
  Ninja, Shell), automatically clean built files, or more.

# OPTIONS
  `-B, --always-build`

:    Unconditionally build all targets.

  `--cache string`

:    Directory for caching internal build information (default ".").

  `-C, --directory string`

:    Run command from directory.

  `-n, --dry-run`

:    Print commands without actually executing.

  `-f, --file string`

:    Knitfile to use (default "knitfile").

  `--hash`

:    Hash files to determine if they are out-of-date (default true).

  `-h, --help`

:    Show a help message.

  `-q, --quiet`

:    Don't print commands when executing.

  `-s, --style string`

:    Printer style to use (basic, steps, progress) (default "basic").

  `-j, --threads int`

:    Number of cores to use.

  `-t, --tool string`

:    Subtool to invoke (use '-t list' to list subtools); further flags are passed to the subtool.

  `-u, --updated strings`

:    Treat given files as updated.

  `-v, --version`

:    Show version information.

# ADDITIONAL DOCUMENTATION
  See more documentation and tutorials online at <https://github.com/zyedidia/knit/tree/master/docs>.

# BUGS

See GitHub Issues: <https://github.com/zyedidia/knit/issues>

# AUTHOR

Zachary Yedidia <zyedidia@gmail.com>

# SEE ALSO

**make(1)**
