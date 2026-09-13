package interp

import "go/ast"

func interfaceEmbeddedNames(it *ast.InterfaceType) []string {
	var names []string
	if it.Methods != nil {
		for _, field := range it.Methods.List {
			if len(field.Names) == 0 {
				names = append(names, typeString(field.Type))
			}
		}
	}
	return names
}

// Resolve embedded named interfaces lazily so declarations may refer to
// later types. The common non-embedded assertion retains its direct fast path.
func (vm *Interpreter) valueSatisfiesInterface(value any, td *TypeDef, visiting map[*TypeDef]bool) bool {
	if !vm.valueSatisfiesMethods(value, td.InterfaceMethods) {
		return false
	}
	if len(td.InterfaceEmbeds) == 0 {
		return true
	}
	if visiting == nil {
		visiting = make(map[*TypeDef]bool)
	}
	if visiting[td] {
		return false
	} // Reject invalid embedding cycles.
	visiting[td] = true
	defer delete(visiting, td)
	for _, name := range td.InterfaceEmbeds {
		if name == "any" {
			continue
		}
		if name == "error" {
			if _, ok := value.(error); ok {
				continue
			}
			if !vm.valueSatisfiesMethods(value, []string{"Error"}) {
				return false
			}
			continue
		}
		embedded := vm.types[name]
		if embedded == nil || embedded.Kind != "interface" || !vm.valueSatisfiesInterface(value, embedded, visiting) {
			return false
		}
	}
	return true
}
