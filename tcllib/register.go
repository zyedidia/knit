package tcllib

import (
	"errors"
	"fmt"
	"reflect"
	"unicode/utf8"

	tcl "github.com/zyedidia/gotcl"
)

var errInterface reflect.Type

func init() {
	errInterface = reflect.TypeOf((*error)(nil)).Elem()
}

func makeSlice[T any](t reflect.Type, arg *tcl.TclObj) ([]T, error) {
	args, err := arg.AsList()
	vals := make([]T, len(args))
	if err != nil {
		return nil, err
	}
	elemt := t.Elem()
	for i := 0; i < len(vals); i++ {
		v, err := tclToGo(elemt, args[i])
		if err != nil {
			return nil, err
		}
		vals[i] = v.(T)
	}
	return vals, nil
}

func tclToGo(t reflect.Type, arg *tcl.TclObj) (any, error) {
	switch t.Kind() {
	case reflect.String:
		return arg.AsString(), nil
	case reflect.Int:
		num, err := arg.AsInt()
		if err != nil {
			return nil, fmt.Errorf("could not parse int: %w", err)
		}
		return num, nil
	case reflect.Int32: // rune
		// ignore size
		r, _ := utf8.DecodeRuneInString(arg.AsString())
		return r, nil
	case reflect.Slice:
		switch t.Elem().Kind() {
		case reflect.String:
			return makeSlice[string](t, arg)
		case reflect.Int:
			return makeSlice[int](t, arg)
		}
	}
	return nil, fmt.Errorf("type %v cannot be converted", t.Kind())
}

func goToTcl(v reflect.Value) (*tcl.TclObj, error) {
	switch v.Kind() {
	case reflect.Interface:
		if v.Type().Implements(errInterface) {
			if v.IsNil() {
				return tcl.FromInt(0), nil
			} else {
				return nil, errors.New(fmt.Sprintf("%v", v))
			}
		}
	case reflect.Int:
		return tcl.FromInt(int(v.Int())), nil
	case reflect.String:
		return tcl.FromStr(v.String()), nil
	case reflect.Slice:
		switch v.Type().Elem().Kind() {
		case reflect.Int:
			return tcl.FromIntList(v.Interface().([]int)), nil
		case reflect.String:
			return tcl.FromList(v.Interface().([]string)), nil
		}
	}
	return nil, fmt.Errorf("cannot convert %v to tcl", v.Kind())
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
			val, err := tclToGo(t.In(i), args[i])
			if err != nil {
				return itp.Fail(err)
			}
			argv = append(argv, reflect.ValueOf(val))
		}
		ret := v.Call(argv)

		switch len(ret) {
		case 0:
			return 0
		case 2:
			_, err := goToTcl(ret[1])
			if err != nil {
				return itp.Fail(err)
			}
			fallthrough
		case 1:
			val, err := goToTcl(ret[0])
			if err != nil {
				return itp.Fail(err)
			}
			return itp.Return(val)
		}
		return 0
	}
	interp.SetCmd(name, cmd)
}
