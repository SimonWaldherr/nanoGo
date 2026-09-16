# Reusable Go-subset packages

nanoGo evaluates a supported subset of Go. The executable reference suite in
`compat/` runs deterministic programs with the installed Go toolchain and with
both `Interpreter.RunContext` and the module loader. It also verifies that both
execution paths reject selected invalid numeric argument conversions. Run it
with `go test ./compat`.

## Numeric arguments

Guest function calls apply a declared numeric parameter type to assignable
untyped constants. Integer-looking constants passed to `float64` parameters
therefore participate in floating-point arithmetic:

```go
const factor = 3
func half(x float64) float64 { return x / 2 }

func main() {
    fmt.Println(half(1), half(factor), half(1+2)) // 0.5 1.5 1.5
    n := 3
    fmt.Println(half(float64(n))) // explicit conversion for a typed int
}
```

This applies to ordinary calls, closures, methods, package functions, variadic
arguments, and calls captured by `defer` or `go`. Untyped constants exported by
loaded packages retain that property. Explicitly typed constants are typed
values: `const n int = 1` does not make `half(n)` valid.

The interpreter rejects the tested typed `int`/floating-point/narrow-integer
argument mismatches, fractional constants passed to integer parameters, and
out-of-range narrow integer constants. This is a deliberate tightening of the
previous dynamic argument behavior. Add the same explicit conversion that Go
requires when migrating those calls. Native callbacks retain their existing
dynamic argument API; the typed host bridge has its own validation contract.

This is not a complete Go type checker. Primitive integer widths still use the
interpreter's `int` storage, and floating-point storage uses `float64`. Declared
variable, parameter, field, and conversion types preserve additional context,
but complete static type propagation through every expression and return-value
path is not implemented. Arbitrary-precision compile-time constants and all
invalid-Go rejection rules are outside this contract.

## Nil containers and nested literals

The zero value of `[]T` and `map[K]V` retains its element/key type and nil backing
storage. It has length zero, compares equal to nil, and can be ranged over.
Appending to a nil slice works. An empty slice literal is non-nil. Reading a
missing map entry gives its element's zero value, while writing a nil map raises
a recoverable guest panic. Setting a map entry to nil stores an entry; use
`delete` to remove it.

`append` returns a new slice header. It preserves Go's distinction between the
header's length and its shared backing array. A function appending to its slice
parameter does not change the caller's slice length; element writes may still
be visible when storage is shared.

Composite literals supply their element type to nested literals without
rewriting the parsed AST. Examples include:

```go
type Point struct { X, Y float64 }

points := [][]float64{{0, 0}, {1, 1}}
groups := map[string][]int{"first": {1, 2}}
records := []Point{{X: 1, Y: 2}, {3, 4}}
```

Both keyed and positional supported struct literals initialize fields with
their declared types. String-to-`[]byte` and string-to-`[]rune` conversions copy
the string contents into guest slices; `[]byte(nil)` preserves nil storage.
Pointers to declared variables and fields retain their declared target type,
including numeric widths used by JSON decoding.

## Package identity and aliases

Loaded packages register types using the resolved package directory plus the
declared type name. The import alias is only a lexical name. Independent
packages may therefore both declare `Value`, even when their Go package names
are identical: methods, zero values, literals, and type assertions resolve to
the correct package. The reference test `TestPackageTypeIdentity` covers this
case with two imported packages and differing methods.

Type aliases (`type Alias = Original`) retain the original identity. Named
scalar definitions (`type Count int`) have their own identity, including across
interfaces and arithmetic. Hosts inspecting raw interpreter values may now see
`interp.NamedValue{TypeName, Value}` for named scalars; `interp.ToNativeValue`
returns the underlying ordinary Go value. This replaces the old conflation of
named scalar definitions with aliases.

Hosts constructing package scopes directly can call
`NewPackageScopeWithIdentity(name, identity)` with a distinct, stable identity.
The existing `NewPackageScope(name)` remains available and uses `name` as that
identity. Separate scopes using the same identity intentionally refer to the
same namespace; use distinct identities for unrelated packages.

Directly declared structs, named scalars, and aliases are covered here. Go's
complete named-type/underlying-type assignability rules, named container types,
generic named types, embedded-field promotion, and full interface boxing are
not claimed. In particular, nanoGo does not yet reproduce every distinction
between a nil interface and an interface holding a typed nil container.

## Error values

An ordinary returned native Go error is usable as an `error` guest value.
`err.Error()`, an extracted `err.Error` method value, and deferred calls to it
work without reflective access to arbitrary host methods. Interpreter failures
remain a separate execution error channel; JSON's standard facade returns
ordinary serialization errors to guest code.

## Verification boundaries

The compatibility programs avoid map iteration order, scheduling assumptions,
and timing thresholds. They cover successful direct and loaded execution,
invalid numeric calls, and package-name collisions. They are a semantic subset
reference against the installed Go compiler, not a guarantee that every Go
program compiles or that historical nanoGo releases behave identically. Native
test results alone do not establish browser or WASM behavior; those require the
separate actual-WASM and browser checks described in the embedding guides.
