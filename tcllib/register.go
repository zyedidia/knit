package tcllib

import (
	"fmt"
	"reflect"
	"unicode/utf8"

	tcl "github.com/zyedidia/gotcl"
)

var errInterface reflect.Type

func init() {
	errInterface = reflect.TypeOf((*error)(nil)).Elem()
}
func register(interp *tcl.Interp, name string, fn interface{}) {
	v := reflect.ValueOf(fn)
	t := v.Type()

	cmd := func(itp *tcl.Interp, args []*tcl.TclObj) tcl.TclStatus {
		if len(args) != t.NumIn() {
			return itp.Fail(fmt.Errorf("invalid number of arguments. got: %v, want %v", len(args), t.NumIn()))
		}

		argv := make([]reflect.Value, 0, len(args))

		for i := range args {
			switch t.In(i).Kind() {
			case reflect.String:
				argv = append(argv, reflect.ValueOf(args[i].AsString()))
			case reflect.Int:
				num, err := args[i].AsInt()
				if err != nil {
					return itp.Fail(fmt.Errorf("expected 'int' for argument %d (parse error %w)", i+1, err))
				}
				argv = append(argv, reflect.ValueOf(num))
			case reflect.Int32: // rune
				// ignore size
				r, _ := utf8.DecodeRuneInString(args[i].AsString())
				argv = append(argv, reflect.ValueOf(r))
			}
		}
		ret := v.Call(argv)

		if len(ret) == 0 {
			return 0
		} else if len(ret) == 1 {
			switch ret[0].Kind() {
			case reflect.Interface:
				if ret[0].Type().Implements(errInterface) && !ret[0].IsNil() {
					return itp.FailStr(fmt.Sprintf("%v", ret[0]))
				}
				return itp.Return(tcl.FromStr(""))
			case reflect.Int:
				return itp.Return(tcl.FromInt(int(ret[0].Int())))
			case reflect.String:
				return itp.Return(tcl.FromStr(ret[0].String()))
			case reflect.Slice:
				switch ret[0].Type().Elem().Kind() {
				case reflect.Int:
					return itp.Return(tcl.FromIntList(ret[0].Interface().([]int)))
				case reflect.String:
					return itp.Return(tcl.FromList(ret[0].Interface().([]string)))
				}
			}
		} else if len(ret) == 2 {
			switch ret[1].Kind() {
			case reflect.Interface:
				if ret[1].Type().Implements(errInterface) && !ret[1].IsNil() {
					return itp.FailStr(fmt.Sprintf("%v", ret[1]))
				}
			}
			switch ret[0].Kind() {
			case reflect.Int:
				return itp.Return(tcl.FromInt(int(ret[0].Int())))
			case reflect.String:
				return itp.Return(tcl.FromStr(ret[0].String()))
			case reflect.Slice:
				switch ret[0].Type().Elem().Kind() {
				case reflect.Int:
					return itp.Return(tcl.FromIntList(ret[0].Interface().([]int)))
				case reflect.String:
					return itp.Return(tcl.FromList(ret[0].Interface().([]string)))
				}
			}
		}
		return 0
	}
	interp.SetCmd(name, cmd)
}
