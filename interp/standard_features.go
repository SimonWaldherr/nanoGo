package interp

import (
	cryptorand "crypto/rand"
	"crypto/sha256"
	"fmt"
	"os"
	"strings"
)

func validEnvironmentKey(key string) bool {
	return key != "" && !strings.ContainsAny(key, "=\x00")
}

// os.Expand is purely lexical; the resolver is exclusively the virtual env.
func expandEnvironment(text string, lookup func(string) string) string {
	return os.Expand(text, lookup)
}

func nativeSprintf(args []any) (any, error) {
	return fmt.Sprintf(ToString(args[0]), args[1:]...), nil
}

func registerCryptoRandPackage(vm *Interpreter) {
	pkg := &Package{Name: "crypto/rand", Funcs: map[string]*Function{}}
	pkg.Funcs["Read"] = &Function{Name: "Read", Params: []string{"p"}, Native: func(args []any) (any, error) {
		if args[0] == nil {
			return 0, nil
		}
		buffer, ok := args[0].(*SliceVal)
		if !ok || buffer == nil || !isByteType(buffer.ElementType) {
			return nil, NewRuntimeError("crypto/rand.Read: expected []byte")
		}
		data := make([]byte, len(buffer.Data))
		n, err := cryptorand.Read(data)
		for i := 0; i < n; i++ {
			buffer.Data[i] = int(data[i])
		}
		return n, err
	}}
	vm.RegisterPackage(pkg.Name, pkg)
}

func registerSHA256Package(vm *Interpreter) {
	pkg := &Package{Name: "crypto/sha256", Vars: map[string]any{"Size": sha256.Size, "BlockSize": sha256.BlockSize}, Funcs: map[string]*Function{}}
	pkg.Funcs["Sum256"] = &Function{Name: "Sum256", Params: []string{"data"}, Native: func(args []any) (any, error) {
		sum := sha256.Sum256(binaryArg(args[0]))
		value := byteSliceValue(sum[:])
		value.Fixed = true
		return value, nil
	}}
	vm.RegisterPackage(pkg.Name, pkg)
}
