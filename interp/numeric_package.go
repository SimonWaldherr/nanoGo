package interp

import (
	"fmt"
	"math"
	"math/big"
	"regexp"
	"strings"
)

// Exact values cross the guest/host boundary as ordinary structs containing
// decimal strings. No native big.Int/Rat pointers escape to guest code.
const numericTextLimit = 256
const numericScaleLimit = 128

var numericDecimalSyntax = regexp.MustCompile(`^[+-]?([0-9]+(\.[0-9]*)?|\.[0-9]+)$`)
var numericIntegerSyntax = regexp.MustCompile(`^[+-]?[0-9]+$`)

func numericError(message string) error { return NewRuntimeError("numeric: " + message) }
func numericString(v any, integer bool) (string, error) {
	s, ok := v.(string)
	if !ok || len(s) == 0 || len(s) > numericTextLimit {
		return "", numericError("expected a numeric string of at most 256 characters")
	}
	syntax := numericDecimalSyntax
	if integer {
		syntax = numericIntegerSyntax
	}
	if !syntax.MatchString(s) {
		return "", numericError("invalid numeric string")
	}
	return s, nil
}
func numericRat(v any, integer bool) (*big.Rat, error) {
	s, err := numericString(v, integer)
	if err != nil {
		return nil, err
	}
	r, ok := new(big.Rat).SetString(s)
	if !ok {
		return nil, numericError("invalid number")
	}
	return r, nil
}
func numericValue(kind string, fields map[string]any) *StructVal {
	return &StructVal{TypeName: "numeric." + kind, Fields: fields}
}
func numericField(v *StructVal, name string) any { value, _ := v.field(name); return value }
func numericCurrency(v any) (string, error) {
	s, ok := v.(string)
	if !ok || len(s) != 3 {
		return "", numericError("currency must be three uppercase ASCII letters")
	}
	for _, c := range s {
		if c < 'A' || c > 'Z' {
			return "", numericError("currency must be three uppercase ASCII letters")
		}
	}
	return s, nil
}
func numericDecimalText(r *big.Rat) (string, error) {
	denominator := new(big.Int).Set(r.Denom())
	five := big.NewInt(5)
	rem := new(big.Int)
	a, b := 0, 0
	for denominator.Bit(0) == 0 {
		denominator.Rsh(denominator, 1)
		a++
		if a > numericScaleLimit {
			return "", numericError("decimal scale exceeds 128 places")
		}
	}
	for {
		rem.Mod(denominator, five)
		if rem.Sign() != 0 {
			break
		}
		denominator.Div(denominator, five)
		b++
		if b > numericScaleLimit {
			return "", numericError("decimal scale exceeds 128 places")
		}
	}
	if denominator.Cmp(big.NewInt(1)) != 0 {
		return "", numericError("non-terminating decimal; use Rational and Fixed for explicit rounding")
	}
	scale := max(a, b)
	s := r.FloatString(scale)
	if scale > 0 {
		s = strings.TrimRight(strings.TrimRight(s, "0"), ".")
	}
	if len(s) > numericTextLimit {
		return "", numericError("result exceeds 256 characters")
	}
	return s, nil
}
func numericExact(v any) (*big.Rat, string, string, error) {
	sv, ok := v.(*StructVal)
	if !ok || sv == nil {
		return nil, "", "", numericError("expected an exact numeric value")
	}
	kind := strings.TrimPrefix(sv.TypeName, "numeric.")
	if sv.TypeName != "numeric."+kind {
		return nil, "", "", numericError("invalid numeric type")
	}
	var r *big.Rat
	var err error
	currency := ""
	switch kind {
	case "Decimal", "BigInt":
		r, err = numericRat(numericField(sv, "Value"), kind == "BigInt")
	case "Money":
		currency, err = numericCurrency(numericField(sv, "Currency"))
		if err != nil {
			return nil, "", "", err
		}
		r, err = numericRat(numericField(sv, "Amount"), false)
	case "Rational":
		n, e := numericRat(numericField(sv, "Numerator"), true)
		if e != nil {
			return nil, "", "", e
		}
		d, e := numericRat(numericField(sv, "Denominator"), true)
		if e != nil {
			return nil, "", "", e
		}
		if d.Sign() == 0 {
			return nil, "", "", numericError("zero denominator")
		}
		r = new(big.Rat).Quo(n, d)
	default:
		return nil, "", "", numericError("expected Decimal, BigInt, Rational or Money")
	}
	return r, kind, currency, err
}
func numericExactResult(r *big.Rat, kind, currency string) (any, error) {
	// Bound growth before decimal formatting or further operations.
	if r.Num().BitLen() > 1024 || r.Denom().BitLen() > 1024 {
		return nil, numericError("numeric precision limit exceeded")
	}
	switch kind {
	case "BigInt":
		if !r.IsInt() {
			return nil, numericError("BigInt result is not an integer")
		}
		s := r.Num().String()
		if len(s) > numericTextLimit {
			return nil, numericError("result exceeds 256 characters")
		}
		return numericValue(kind, map[string]any{"Value": s}), nil
	case "Rational":
		n, d := r.Num().String(), r.Denom().String()
		if len(n) > numericTextLimit || len(d) > numericTextLimit {
			return nil, numericError("result exceeds 256 characters")
		}
		return numericValue(kind, map[string]any{"Numerator": n, "Denominator": d}), nil
	default:
		s, err := numericDecimalText(r)
		if err != nil {
			return nil, err
		}
		if kind == "Money" {
			return numericValue(kind, map[string]any{"Amount": s, "Currency": currency}), nil
		}
		return numericValue("Decimal", map[string]any{"Value": s}), nil
	}
}

func registerNumericPackage(vm *Interpreter) {
	pkg := &Package{Name: "numeric", Funcs: map[string]*Function{}, Types: map[string]*TypeDef{}}
	makeNative := func(name string, arity int, body func([]any) (any, error)) *Function {
		return &Function{Name: name, Native: func(args []any) (any, error) {
			if len(args) != arity {
				expected := arity
				if strings.Contains(name, ".") {
					expected--
				}
				return nil, numericError(fmt.Sprintf("%s expects %d arguments", name, expected))
			}
			return body(args)
		}}
	}
	addType := func(name string, fields ...FieldDef) *TypeDef {
		td := &TypeDef{Name: "numeric." + name, Kind: "struct", Fields: fields, Methods: map[string]*Function{}}
		vm.types[td.Name] = td
		pkg.Types[name] = td
		return td
	}
	for _, kind := range []string{"Decimal", "BigInt", "Rational", "Money"} {
		fields := []FieldDef{newFieldDef("Value", "string", "")}
		if kind == "Money" {
			fields = []FieldDef{newFieldDef("Amount", "string", ""), newFieldDef("Currency", "string", "")}
		}
		if kind == "Rational" {
			fields = []FieldDef{newFieldDef("Numerator", "string", ""), newFieldDef("Denominator", "string", "")}
		}
		td := addType(kind, fields...)
		arity := 1
		if kind == "Money" || kind == "Rational" {
			arity = 2
		}
		pkg.Funcs[kind] = makeNative(kind, arity, func(args []any) (any, error) {
			r, err := numericRat(args[0], kind == "BigInt" || kind == "Rational")
			if err != nil {
				return nil, err
			}
			currency := ""
			if kind == "Money" {
				currency, err = numericCurrency(args[1])
				if err != nil {
					return nil, err
				}
			}
			if kind == "Rational" {
				d, e := numericRat(args[1], true)
				if e != nil {
					return nil, e
				}
				if d.Sign() == 0 {
					return nil, numericError("zero denominator")
				}
				r.Quo(r, d)
			}
			return numericExactResult(r, kind, currency)
		})
		for _, op := range []string{"Add", "Sub", "Mul", "Div", "Cmp"} {
			td.Methods[op] = makeNative(kind+"."+op, 2, func(args []any) (any, error) {
				a, k, c, err := numericExact(args[0])
				if err != nil {
					return nil, err
				}
				b, bk, bc, err := numericExact(args[1])
				if err != nil {
					return nil, err
				}
				if k == "Money" {
					if op == "Add" || op == "Sub" || op == "Cmp" {
						if bk != "Money" || c != bc {
							return nil, numericError("money operation requires matching currencies")
						}
					} else if bk == "Money" {
						return nil, numericError("money scaling requires a dimensionless exact number")
					}
				} else if bk == "Money" {
					return nil, numericError("cannot mix money and dimensionless numbers")
				}
				if k == "BigInt" && bk != "BigInt" {
					return nil, numericError("BigInt operation requires BigInt")
				}
				r := new(big.Rat)
				switch op {
				case "Cmp":
					return a.Cmp(b), nil
				case "Add":
					r.Add(a, b)
				case "Sub":
					r.Sub(a, b)
				case "Mul":
					r.Mul(a, b)
				case "Div":
					if b.Sign() == 0 {
						return nil, numericError("division by zero")
					}
					if k == "BigInt" {
						r.SetInt(new(big.Int).Quo(a.Num(), b.Num()))
					} else {
						r.Quo(a, b)
					}
				}
				return numericExactResult(r, k, c)
			})
		}
		td.Methods["String"] = makeNative(kind+".String", 1, func(args []any) (any, error) {
			r, k, c, e := numericExact(args[0])
			if e != nil {
				return nil, e
			}
			if k == "Rational" {
				return r.RatString(), nil
			}
			s, e := numericDecimalText(r)
			if e != nil {
				return nil, e
			}
			if k == "Money" {
				s = c + " " + s
			}
			return s, nil
		})
		td.Methods["Fixed"] = makeNative(kind+".Fixed", 2, func(args []any) (any, error) {
			r, _, _, e := numericExact(args[0])
			if e != nil {
				return nil, e
			}
			places, ok := args[1].(int)
			if !ok || places < 0 || places > numericScaleLimit {
				return nil, numericError("Fixed requires 0..128 places")
			}
			return r.FloatString(places), nil
		})
		for _, op := range []string{"Neg", "Abs"} {
			td.Methods[op] = makeNative(kind+"."+op, 1, func(args []any) (any, error) {
				r, k, c, e := numericExact(args[0])
				if e != nil {
					return nil, e
				}
				if op == "Neg" {
					r.Neg(r)
				} else {
					r.Abs(r)
				}
				return numericExactResult(r, k, c)
			})
		}
	}
	registerNumericVectors(vm, pkg, makeNative, addType)
	vm.RegisterPackage("numeric", pkg)
}

type numericNativeBuilder func(string, int, func([]any) (any, error)) *Function
type numericTypeBuilder func(string, ...FieldDef) *TypeDef

func numericFloat(v any) (float64, error) {
	var f float64
	switch x := v.(type) {
	case int:
		f = float64(x)
	case int64:
		f = float64(x)
	case float64:
		f = x
	default:
		return 0, numericError("coordinate must be int or float64")
	}
	if math.IsNaN(f) || math.IsInf(f, 0) {
		return 0, numericError("non-finite coordinate or vector result")
	}
	return f, nil
}
func numericVector(v any, kind string) ([]float64, error) {
	sv, ok := v.(*StructVal)
	if !ok || sv == nil || sv.TypeName != "numeric."+kind {
		return nil, numericError("expected " + kind)
	}
	names := []string{"X", "Y"}
	if kind == "Vec3" {
		names = append(names, "Z")
	}
	out := make([]float64, len(names))
	for i, n := range names {
		f, e := numericFloat(numericField(sv, n))
		if e != nil {
			return nil, e
		}
		out[i] = f
	}
	return out, nil
}
func numericVectorResult(kind string, values []float64) (any, error) {
	fields := map[string]any{}
	names := []string{"X", "Y", "Z"}
	for i, v := range values {
		if _, e := numericFloat(v); e != nil {
			return nil, e
		}
		fields[names[i]] = v
	}
	return numericValue(kind, fields), nil
}
func numericNorm(v []float64) float64 {
	n := math.Hypot(v[0], v[1])
	if len(v) == 3 {
		n = math.Hypot(n, v[2])
	}
	return n
}
func registerNumericVectors(vm *Interpreter, pkg *Package, native numericNativeBuilder, addType numericTypeBuilder) {
	for _, kind := range []string{"Vec2", "Vec3", "Coordinate"} {
		size := 2
		if kind == "Vec3" {
			size = 3
		}
		fields := []FieldDef{newFieldDef("X", "float64", ""), newFieldDef("Y", "float64", "")}
		if size == 3 {
			fields = append(fields, newFieldDef("Z", "float64", ""))
		}
		td := addType(kind, fields...)
		pkg.Funcs[kind] = native(kind, size, func(args []any) (any, error) {
			values := make([]float64, size)
			for i, v := range args {
				f, e := numericFloat(v)
				if e != nil {
					return nil, e
				}
				values[i] = f
			}
			return numericVectorResult(kind, values)
		})
		for _, op := range []string{"Add", "Sub", "Scale", "Dot", "Norm", "Normalize", "Distance", "Cross", "Translate"} {
			if kind == "Coordinate" && op != "Sub" && op != "Distance" && op != "Translate" {
				continue
			}
			if kind != "Coordinate" && op == "Translate" {
				continue
			}
			if op == "Cross" && size != 3 {
				continue
			}
			arity := 2
			if op == "Norm" || op == "Normalize" {
				arity = 1
			}
			td.Methods[op] = native(kind+"."+op, arity, func(args []any) (any, error) {
				a, e := numericVector(args[0], kind)
				if e != nil {
					return nil, e
				}
				if op == "Norm" {
					return numericFloat(numericNorm(a))
				}
				if op == "Normalize" || op == "Scale" {
					f := 0.0
					if op == "Normalize" {
						scale := 0.0
						for _, component := range a {
							scale = math.Max(scale, math.Abs(component))
						}
						if scale == 0 {
							return nil, numericError("cannot normalize zero vector")
						}
						for i := range a {
							a[i] /= scale
						}
						n := numericNorm(a)
						for i := range a {
							a[i] /= n
						}
					} else {
						f, e = numericFloat(args[1])
						if e != nil {
							return nil, e
						}
						for i := range a {
							a[i] *= f
						}
					}
					return numericVectorResult(kind, a)
				}
				otherKind := kind
				if op == "Translate" {
					otherKind = "Vec2"
				}
				b, e := numericVector(args[1], otherKind)
				if e != nil {
					return nil, e
				}
				if op == "Dot" {
					v := 0.0
					for i := range a {
						v += a[i] * b[i]
					}
					return numericFloat(v)
				}
				if op == "Cross" {
					return numericVectorResult(kind, []float64{a[1]*b[2] - a[2]*b[1], a[2]*b[0] - a[0]*b[2], a[0]*b[1] - a[1]*b[0]})
				}
				for i := range a {
					if op == "Add" || op == "Translate" {
						a[i] += b[i]
					} else {
						a[i] -= b[i]
					}
				}
				if op == "Distance" {
					return numericFloat(numericNorm(a))
				}
				resultKind := kind
				if kind == "Coordinate" && op == "Sub" {
					resultKind = "Vec2"
				}
				return numericVectorResult(resultKind, a)
			})
		}
	}
}

// Explicit methods prevent a domain value from silently coercing to zero in
// the legacy dynamic scalar operators. Equality retains struct value semantics.
func isNumericValue(value any) bool {
	sv, ok := value.(*StructVal)
	if !ok || sv == nil {
		return false
	}
	switch sv.TypeName {
	case "numeric.Decimal", "numeric.BigInt", "numeric.Rational", "numeric.Money", "numeric.Vec2", "numeric.Vec3", "numeric.Coordinate":
		return true
	}
	return false
}
