package knit

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"unicode"
	"unicode/utf8"

	lua "github.com/zyedidia/gopher-lua"
)

func title(s string) string {
	r, size := utf8.DecodeRuneInString(s)
	return string(unicode.ToTitle(r)) + s[size:]
}

func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

type assign struct {
	name  string
	value string
}

func makeAssigns(args []string) ([]assign, []string) {
	assigns := make([]assign, 0, len(args))
	other := make([]string, 0)
	for _, a := range args {
		before, after, found := strings.Cut(a, "=")
		if found {
			assigns = append(assigns, assign{
				name:  before,
				value: after,
			})
		} else {
			other = append(other, a)
		}
	}
	return assigns, other
}

func goToKnitfile(vm *LuaVM, dir string, targets []string) error {
	wd, err := os.Getwd()
	if err != nil {
		return err
	}
	for i, t := range targets {
		r, err := filepath.Rel(dir, wd)
		if err != nil {
			return err
		}
		if r != "" && r != "." {
			targets[i] = fmt.Sprintf("[%s]%s", r, t)
		}
	}

	return os.Chdir(dir)
}

var ErrNothingToDo = errors.New("nothing to be done")
var ErrQuiet = errors.New("quiet")

type ErrMessage struct {
	msg string
}

func (e *ErrMessage) Error() string {
	return e.msg
}

func Run(out io.Writer, args []string, flags Flags) (string, error) {
	if flags.RunDir != "" {
		os.Chdir(flags.RunDir)
	}

	vm := NewLuaVM()

	cliAssigns, targets := makeAssigns(args)
	envAssigns, _ := makeAssigns(os.Environ())

	vm.MakeTable("cli", cliAssigns)
	vm.MakeTable("env", envAssigns)

	file, dir, err := FindBuildFile(flags.Knitfile)
	if err != nil {
		return "", err
	}
	knitpath := filepath.Join(dir, file)
	if file == "" {
		def, ok := DefaultBuildFile()
		if ok {
			file = def
		}
	} else if dir != "" {
		err = goToKnitfile(vm, dir, targets)
		if err != nil {
			return knitpath, err
		}
	}

	if file == "" {
		return knitpath, fmt.Errorf("%s does not exist", flags.Knitfile)
	}

	lval, err := vm.DoFile(file)
	if err != nil {
		return knitpath, err
	}

	switch v := lval.(type) {
	case lua.LString:
		return knitpath, &ErrMessage{msg: string(v)}
	case *lua.LNilType:
		return knitpath, ErrQuiet
	case *lua.LUserData:
	}

	fmt.Println(lval)

	return knitpath, ErrNothingToDo
}
