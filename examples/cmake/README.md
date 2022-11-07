An example of using Knit as a CMake backend by converting Ninja to Knit. This
feature is **experimental**. Make sure you have
[knitja](https://github.com/zyedidia/knitja) installed.

First run `knit build`. This will run cmake, and convert the `build.ninja` file
to a `Knitfile` using `knitja` (make sure you have `knitja` installed).

Next run `knit all -C build` to run the build using the generated Knitfile.
