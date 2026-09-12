# Exact numbers, money and geometry

Run `go run ./examples/numeric` from the repository root, or load `program.ng`
into the web playground. Import `numeric` after registering builtin packages
in an embedded interpreter.

| Constructor | Fields | Representation |
| --- | --- | --- |
| `Decimal("0.1")` | `Value` | Exact terminating decimal |
| `BigInt("12345678901234567890")` | `Value` | Exact integer |
| `Rational("1", "3")` | `Numerator`, `Denominator` | Reduced exact fraction |
| `Money("19.99", "EUR")` | `Amount`, `Currency` | Exact decimal and currency label |
| `Vec2(3, 4)` | `X`, `Y` | Finite float64 components |
| `Vec3(1, 2, 3)` | `X`, `Y`, `Z` | Finite float64 components |
| `Coordinate(10, 20)` | `X`, `Y` | Cartesian point, not latitude/longitude |

All names in the table are qualified with `numeric.`. Construct exact values
with these functions; their empty struct zero values are not valid numbers.
Methods validate fields, including fields changed by guest code.

## Exact arithmetic

`Decimal`, `BigInt`, `Rational` and `Money` provide `Add`, `Sub`, `Mul`, `Div`,
`Cmp`, `Neg`, `Abs`, `String` and `Fixed`. Operations return new values without
mutating operands. `Cmp` returns -1, 0 or 1. Use it for numeric equality and
ordering; struct equality compares representation. Scalar arithmetic operators
and increment/decrement are not overloaded.

The receiver determines the result type. `BigInt` accepts only `BigInt`
operands and divides toward zero. `Decimal` accepts dimensionless exact types,
but rejects a non-terminating result such as 1/3; use `Rational` for fractions.
`Rational` accepts dimensionless exact types and preserves fractions.

Money addition, subtraction and comparison require matching currency labels.
Money multiplication and division accept dimensionless exact numbers such as
`Decimal("1.19")`; multiplying two amounts is rejected. Labels must have three
uppercase ASCII letters. There is no currency registry, exchange-rate conversion
or automatic currency-specific rounding.

`Fixed(places)` returns a string, rounding ties away from zero. It does not
change the stored value. `Money.String()` includes the currency, whereas
`Money.Fixed(2)` returns only the formatted amount. To store an explicitly rounded
amount, construct `numeric.Money(total.Fixed(2), total.Currency)`.

Inputs use plain decimal/integer strings without exponent notation. Text is
limited to 256 characters per component, decimal scale and `Fixed` precision to
128 places. Results exceeding these limits fail; `BigInt` is not unlimited.
Division by zero and incompatible types produce runtime errors.

## Geometry

Both vector types provide `Add`, `Sub`, `Scale`, `Dot`, `Norm`, `Normalize` and
`Distance`. `Vec3` also provides `Cross`. Vector operations require matching
dimensions; scaling accepts a scalar int or float64. Zero vectors cannot be
normalized. Normalization scales components first to handle very large and very
small finite inputs. Nonfinite inputs and results are rejected.

Points provide `Translate(Vec2)`, `Sub(Coordinate)` returning a `Vec2`, and
`Distance(Coordinate)`. They use Euclidean geometry with no imposed units.

## JSON and host integration

Values are ordinary guest structs, with the capitalized fields above. For
example, JSON encodes money as `{"Amount":"19.99","Currency":"EUR"}`.
`BridgeToHost` exports a map with those fields. Exact values remain strings;
vectors use float64. Importing a host map does not recover the numeric type
automatically: reconstruct it with the corresponding constructor.
