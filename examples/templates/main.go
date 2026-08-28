// templates shows guest code driving Go's text/template through nanoGo's
// curated text/template.RenderString: struct and map data, {{range}} over a
// slice of structs, {{if}}/{{else}}, index variables, the len and printf
// builtins, and how a template parse error reaches the guest as an ordinary
// error value.
//
// RenderString is the whole template surface nanoGo exposes. It takes the
// template text and one data value, and returns the expanded string — the
// data can be a guest struct, slice, or map, which nanoGo converts to plain
// Go values before handing them to text/template. Note that this is
// text/template, not html/template: it does no HTML escaping, so do not
// build HTML from untrusted data with it.
//
// Run it with: go run ./examples/templates
package main

import (
	"fmt"
	"log"
	"os"

	"simonwaldherr.de/go/nanogo/interp"
)

// guestProgram renders a small stock report. Everything here runs inside the
// interpreter, not in this host process.
//
// Two things are worth pointing out for anyone adapting this:
//
//   - The struct types are declared at package level. nanoGo does not
//     support a type declaration inside a function body, so `type Row
//     struct{...}` in main() would fail to resolve.
//   - Composite literals inside a slice literal need their type spelled out
//     (`[]Row{Row{...}, Row{...}}`); Go's elided `[]Row{{...}, {...}}` form
//     is not part of the supported subset.
const guestProgram = `package main

import (
	"fmt"
	"text/template"
)

type Row struct {
	Name  string
	Qty   int
	Price int
}

type Report struct {
	Title string
	Rows  []Row
}

// rowTemplate is rendered once per row. Reusing one template text across
// iterations is the common shape, and nanoGo caches the parsed form, so the
// loop pays the parse cost only once.
const rowTemplate = "{{printf \"%-8s\" .Name}} {{printf \"%3d\" .Qty}} x {{printf \"%4d\" .Price}}{{if eq .Qty 0}}  (out of stock){{end}}"

const summaryTemplate = "{{.Title}} — {{len .Rows}} article(s){{range $i, $r := .Rows}}\n  {{$i}}. {{$r.Name}}{{end}}"

func main() {
	rows := []Row{
		Row{Name: "bolt", Qty: 12, Price: 30},
		Row{Name: "nut", Qty: 0, Price: 10},
		Row{Name: "washer", Qty: 240, Price: 2},
	}

	// 1. One template, rendered once per row.
	fmt.Println("-- rows --")
	total := 0
	for i := 0; i < len(rows); i++ {
		line, err := template.RenderString(rowTemplate, rows[i])
		if err != nil {
			fmt.Println("render error:", err)
			return
		}
		fmt.Println(line)
		total = total + rows[i].Qty*rows[i].Price
	}

	// 2. A struct holding a slice, rendered in one go.
	fmt.Println("-- summary --")
	summary, err := template.RenderString(summaryTemplate, Report{Title: "Warehouse", Rows: rows})
	if err != nil {
		fmt.Println("render error:", err)
		return
	}
	fmt.Println(summary)

	// 3. Map data plus a conditional.
	fmt.Println("-- total --")
	out, err := template.RenderString(
		"total {{.Total}}{{if gt .Total 1000}} (above budget){{else}} (within budget){{end}}",
		map[string]interface{}{"Total": total})
	if err != nil {
		fmt.Println("render error:", err)
		return
	}
	fmt.Println(out)

	// 4. A malformed template is an ordinary error, not a panic.
	fmt.Println("-- error handling --")
	if _, err := template.RenderString("{{.Unclosed", rows); err != nil {
		fmt.Println("bad template reported:", err != nil)
	}
}
`

func main() {
	vm := interp.NewInterpreter()
	interp.RegisterBuiltinPackages(vm)

	// fmt needs exactly these two natives; see examples/quickstart.
	vm.RegisterNative("ConsoleLog", func(args []any) (any, error) {
		if len(args) > 0 {
			fmt.Fprintln(os.Stdout, interp.ToString(args[0]))
		}
		return nil, nil
	})
	vm.RegisterNative("__hostSprintf", func(args []any) (any, error) {
		if len(args) == 0 {
			return "", nil
		}
		return fmt.Sprintf(interp.ToString(args[0]), args[1:]...), nil
	})

	if err := vm.Run(guestProgram); err != nil {
		log.Fatal(err)
	}
	fmt.Printf("\n(guest consumed %d evaluator steps)\n", vm.LastStepCount())
}
