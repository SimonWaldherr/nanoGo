package interp

import (
	"flag"
	"io"
	"strconv"
	"sync"
)

// Each guest FlagSet owns its parser and values. No use of flag.CommandLine,
// host os.Args, host stderr or os.Exit is permitted here.
type guestFlagSet struct {
	mu   sync.Mutex
	set  *flag.FlagSet
	mode int
}

func newGuestFlagSet(name string, mode int) *guestFlagSet {
	set := flag.NewFlagSet(name, flag.ContinueOnError)
	set.SetOutput(io.Discard)
	return &guestFlagSet{set: set, mode: mode}
}

type guestFlagValue struct {
	pointer *PointerVal
	kind    string
}

func (v *guestFlagValue) String() string {
	if v == nil || v.pointer == nil {
		return ""
	}
	return ToString(v.pointer.ref.get())
}
func (v *guestFlagValue) IsBoolFlag() bool { return v.kind == "bool" }
func (v *guestFlagValue) Set(text string) error {
	var value any
	var err error
	switch v.kind {
	case "string":
		value = text
	case "int":
		value, err = strconv.Atoi(text)
	case "bool":
		value, err = strconv.ParseBool(text)
	case "float64":
		value, err = strconv.ParseFloat(text, 64)
	}
	if err != nil {
		return err
	}
	return v.pointer.ref.set(value)
}

func guestFlagSetArg(value any) (*guestFlagSet, error) {
	sv, ok := value.(*StructVal)
	if !ok || sv == nil {
		return nil, NewRuntimeError("flag: expected FlagSet")
	}
	if state := sv.nativeState.Load(); state != nil {
		if flags, ok := state.value.(*guestFlagSet); ok {
			return flags, nil
		}
		return nil, NewRuntimeError("flag: invalid FlagSet state")
	}
	state := &structNativeState{value: newGuestFlagSet("", int(flag.ContinueOnError))}
	sv.nativeState.CompareAndSwap(nil, state)
	return sv.nativeState.Load().value.(*guestFlagSet), nil
}

func registerFlagPackage(vm *Interpreter) {
	td := &TypeDef{Name: "flag.FlagSet", Kind: "struct", Methods: map[string]*Function{}}
	vm.types[td.Name] = td
	// typeString currently erases package qualifiers for type expressions.
	vm.types["FlagSet"] = td
	global := newGuestFlagSet("", int(flag.ContinueOnError))
	var globalMu sync.Mutex
	var globalExecution *execution
	currentGlobal := func() *guestFlagSet {
		globalMu.Lock()
		defer globalMu.Unlock()
		if current := vm.execution.Load(); current != globalExecution {
			global = newGuestFlagSet("", int(flag.ContinueOnError))
			globalExecution = current
		}
		return global
	}
	pkg := &Package{Name: "flag", Funcs: map[string]*Function{}, Types: map[string]*TypeDef{"FlagSet": td}, Vars: map[string]any{
		"ContinueOnError": int(flag.ContinueOnError), "ExitOnError": int(flag.ExitOnError), "PanicOnError": int(flag.PanicOnError), "ErrHelp": flag.ErrHelp,
	}}
	pkg.Funcs["NewFlagSet"] = &Function{Name: "NewFlagSet", Params: []string{"name", "mode"}, Native: func(args []any) (any, error) {
		mode := ToInt(args[1])
		if mode < 0 || mode > int(flag.PanicOnError) {
			return nil, NewRuntimeError("flag: invalid error handling mode")
		}
		value := &StructVal{TypeName: td.Name}
		value.nativeState.Store(&structNativeState{value: newGuestFlagSet(ToString(args[0]), mode)})
		return value, nil
	}}
	register := func(name string, params []string, call func(*guestFlagSet, []any) (any, error)) {
		pkg.Funcs[name] = &Function{Name: name, Params: params, Native: func(args []any) (any, error) {
			set := currentGlobal()
			set.mu.Lock()
			defer set.mu.Unlock()
			return call(set, args)
		}}
		td.Methods[name] = &Function{Name: name, RecvType: td.Name, Params: params, Native: func(args []any) (any, error) {
			set, err := guestFlagSetArg(args[0])
			if err != nil {
				return nil, err
			}
			set.mu.Lock()
			defer set.mu.Unlock()
			return call(set, args[1:])
		}}
	}
	for name, kind := range map[string]string{"String": "string", "Int": "int", "Bool": "bool", "Float64": "float64"} {
		register(name, []string{"name", "value", "usage"}, func(set *guestFlagSet, args []any) (any, error) {
			pointer := newPointerValue(args[1], kind)
			set.set.Var(&guestFlagValue{pointer: pointer, kind: kind}, ToString(args[0]), ToString(args[2]))
			return pointer, nil
		})
		register(name+"Var", []string{"pointer", "name", "value", "usage"}, func(set *guestFlagSet, args []any) (any, error) {
			pointer, ok := args[0].(*PointerVal)
			if !ok || pointer == nil || pointer.ref.kind == lvalueNil || pointer.ElementType != kind {
				return nil, NewRuntimeError("flag: invalid destination pointer")
			}
			if set.set.Lookup(ToString(args[1])) != nil {
				return nil, guestPanic("flag redefined: " + ToString(args[1]))
			}
			if err := pointer.ref.set(args[2]); err != nil {
				return nil, err
			}
			set.set.Var(&guestFlagValue{pointer: pointer, kind: kind}, ToString(args[1]), ToString(args[3]))
			return nil, nil
		})
	}
	register("Set", []string{"name", "value"}, func(set *guestFlagSet, args []any) (any, error) {
		return set.set.Set(ToString(args[0]), ToString(args[1])), nil
	})
	register("Parsed", nil, func(set *guestFlagSet, _ []any) (any, error) { return set.set.Parsed(), nil })
	register("NFlag", nil, func(set *guestFlagSet, _ []any) (any, error) { return set.set.NFlag(), nil })
	register("NArg", nil, func(set *guestFlagSet, _ []any) (any, error) { return set.set.NArg(), nil })
	register("Arg", []string{"index"}, func(set *guestFlagSet, args []any) (any, error) { return set.set.Arg(ToInt(args[0])), nil })
	register("Args", nil, func(set *guestFlagSet, _ []any) (any, error) {
		args := set.set.Args()
		result := &SliceVal{ElementType: "string", Data: make([]any, len(args))}
		for i, arg := range args {
			result.Data[i] = arg
		}
		return result, nil
	})
	td.Methods["Parse"] = &Function{Name: "Parse", RecvType: td.Name, Params: []string{"arguments"}, Native: func(args []any) (any, error) {
		set, err := guestFlagSetArg(args[0])
		if err != nil {
			return nil, err
		}
		var arguments []string
		if args[1] != nil {
			slice, ok := args[1].(*SliceVal)
			if !ok || slice.ElementType != "string" {
				return nil, NewRuntimeError("flag.Parse: expected []string")
			}
			arguments = make([]string, len(slice.Data))
			for i, arg := range slice.Data {
				arguments[i] = ToString(arg)
			}
		}
		set.mu.Lock()
		defer set.mu.Unlock()
		err = set.set.Parse(arguments)
		if err != nil && set.mode == int(flag.PanicOnError) {
			return nil, guestPanic(err)
		}
		if err != nil && set.mode == int(flag.ExitOnError) {
			return nil, NewRuntimeError("flag: " + err.Error())
		}
		return err, nil
	}}
	pkg.Funcs["Parse"] = &Function{Name: "Parse", Native: func([]any) (any, error) {
		set := currentGlobal()
		set.mu.Lock()
		defer set.mu.Unlock()
		var args []string
		if len(vm.Args) > 1 {
			args = vm.Args[1:]
		}
		return nil, set.set.Parse(args)
	}}
	vm.RegisterPackage(pkg.Name, pkg)
}
