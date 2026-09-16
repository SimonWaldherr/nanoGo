package interp

import (
	"context"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"sync"
	"testing"
)

func TestCompoundAssignmentScopesAndEffects(t *testing.T) {
	got := runAndCapture(t, `package main
import "fmt"
func main() {
 a,b,c,d,e:=1000,2000,3000,4000,5000
 a+=7;b-=8;c*=2;d/=4;e%=13
 { a:="inner";a+=":ok";fmt.Println(a) }
 { a:=1.5;a+=2;fmt.Println(a) }
 fmt.Println(a,b,c,d,e)
 f,g,h,j:=1.25,2.5,3.75,5.0
 f+=0.25;g-=0.5;h*=2.0;j/=2.0
 { f:=8;f+=2;fmt.Println(f) }
 fmt.Println(f,g,h,j)
 s:=[]int{10};m:=map[string]int{"v":20};s[0]+=3;m["v"]*=2
 fmt.Println(s[0],m["v"])
 n:=100;calls:=0
 rhs:=func()int{calls++;n=200;return 3}
 n+=rhs()
 fmt.Println(n,calls)
 defer func(){fmt.Println(recover());fmt.Println(n)}()
 n/=0
}`)
	want := "inner:ok\n3.5\n1007 1992 6000 1000 8\n10\n1.5 2 7.5 2.5\n13 40\n203 1\nruntime error: integer divide by zero\n203\n"
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

// Compare unboxed and map-backed bindings at every checkpoint boundary. The
// fallback is also the reference for mixed numeric types and failed division.
func TestCompoundAssignmentStepLimits(t *testing.T) {
	for _, tc := range []struct {
		statement string
		initial   any
	}{
		{"x += (12345 * 7) - 6", 1000},
		{"x *= 1.25 + 0.5", 2.5},
		{"x += 0.5", 1000},
		{"x %= 0", 1000},
		{"x <<= -1", 1000},
		{"x >>= -1", 1000},
		{"x /= -1", -int(^uint(0)>>1) - 1},
		{"x += 1", int(^uint(0) >> 1)},
	} {
		t.Run(tc.statement, func(t *testing.T) {
			file, err := parser.ParseFile(token.NewFileSet(), "compound.go", "package main;func main(){"+tc.statement+"}", 0)
			if err != nil {
				t.Fatal(err)
			}
			stmt := file.Decls[0].(*ast.FuncDecl).Body.List[0]
			for limit := uint64(1); limit < 20; limit++ {
				var results [2]string
				for mode := range results {
					vm := NewInterpreter()
					vm.Limits.MaxSteps = limit
					env := NewEnv(nil)
					if mode == 0 {
						vm.declare("x", tc.initial, env)
					} else {
						env.Vars = map[string]any{"x": tc.initial}
					}
					exec, err := vm.beginExecution(context.Background())
					if err != nil {
						t.Fatal(err)
					}
					_, evalErr := vm.evalStmt(stmt, env)
					exec.finish()
					vm.endExecution(exec)
					value, _ := vm.get("x", env)
					results[mode] = fmt.Sprintf("%T:%v; %v; %d", value, value, evalErr, vm.LastStepCount())
				}
				if results[0] != results[1] {
					t.Fatalf("limit %d: optimized=%s, fallback=%s", limit, results[0], results[1])
				}
			}
		})
	}
}

func TestCompoundAssignmentTracker(t *testing.T) {
	vm := NewInterpreter()
	tracker := NewVariableTracker()
	vm.SetVariableTracker(tracker)
	if err := vm.Run(`package main;func main(){ f:=1.25;f+=0.5;f*=2.0 }`); err != nil {
		t.Fatal(err)
	}
	for _, snapshot := range tracker.Snapshots() {
		if snapshot.Name == "f" && snapshot.Function == "main" {
			if snapshot.Value != "3.5" || snapshot.Writes != 3 {
				t.Fatalf("unexpected float snapshot: %+v", snapshot)
			}
			return
		}
	}
	t.Fatal("no float snapshot recorded")
}

func TestCompoundAssignmentSharedScopes(t *testing.T) {
	for _, concurrent := range []bool{false, true} {
		vm := NewInterpreter()
		env := NewEnv(nil)
		env.shared = !concurrent
		for _, name := range []string{"a", "b", "c", "d"} {
			vm.declareInt(name, 0, env)
		}
		vm.declareFloat("f", 0, env)
		if concurrent {
			exec := &execution{}
			exec.concurrent.Store(true)
			vm.activeExecution = exec
		}
		child := NewEnv(env)
		var wg sync.WaitGroup
		for g := 0; g < 8; g++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for i := 0; i < 1000; i++ {
					vm.compoundNumeric("a", token.ADD, 1, child)
					vm.compoundNumeric("d", token.SUB, 1, child)
					vm.compoundNumeric("f", token.ADD, 0.5, child)
				}
			}()
		}
		wg.Wait()
		for name, want := range map[string]any{"a": 8000, "d": -8000, "f": 4000.0} {
			if got, _ := vm.get(name, env); got != want {
				t.Fatalf("%s=%v want %v", name, got, want)
			}
		}
	}
}

func BenchmarkCompoundAssignments(b *testing.B) {
	for _, workload := range []struct{ name, body string }{
		{"Int", `a,b,c,d:=12345,23456,34567,45678; for i:=0;i<10000;i++ {a+=i;b-=i;c^=i;d|=i}`},
		{"Float", `a,b,c:=1.25,2.5,3.75; for i:=0;i<10000;i++ {a+=0.125;b-=0.0625;c*=1.00001}`},
	} {
		b.Run(workload.name, func(b *testing.B) {
			vm := NewInterpreter()
			src := "package main;func main(){" + workload.body + "}"
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if err := vm.Run(src); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

// Use the ordinary RHS evaluator as an independent reference, including cached
// literals and identifier operands at every possible checkpoint boundary.
func TestCompoundAtomMatchesDynamicEvaluation(t *testing.T) {
	for _, tc := range []struct {
		statement string
		x, y      any
	}{
		{"x += y", 1000, 12345}, {"x -= y", 1000, 12345},
		{"x *= y", 2.5, 1.25}, {"x /= y", 1000, 0},
		{"x %= y", 1000, 0}, {"x <<= y", 1000, -1},
		{"x >>= y", 1000, -1}, {"x &^= y", 1000, 12},
		{"x += y", 1000, 0.5}, {"x += y", 0.5, 1000},
		{"x += y", "hello", "world"},
		{"x += 12345", 1000, nil}, {"x *= 1.25", 2.5, nil},
		{"x /= 0", 1000, nil}, {"x += x", 1000, nil},
	} {
		t.Run(tc.statement+fmt.Sprintf("/%T/%T", tc.x, tc.y), func(t *testing.T) {
			file, err := parser.ParseFile(token.NewFileSet(), "atom.go", "package main;func main(){"+tc.statement+"}", 0)
			if err != nil {
				t.Fatal(err)
			}
			stmt := file.Decls[0].(*ast.FuncDecl).Body.List[0].(*ast.AssignStmt)
			for limit := uint64(1); limit < 6; limit++ {
				var results [2]string
				for mode := range results {
					vm := NewInterpreter()
					vm.Limits.MaxSteps = limit
					env := NewEnv(nil)
					vm.declare("x", tc.x, env)
					vm.declare("y", tc.y, env)
					exec, err := vm.beginExecution(context.Background())
					if err != nil {
						t.Fatal(err)
					}
					exec.litCache, exec.floatLitCache, exec.valueLitCache, exec.typeStrCache = buildLitCaches(file)
					var evalErr error
					if mode == 0 {
						_, evalErr = vm.evalStmtNode(stmt, env)
					} else {
						evalErr = vm.executionError()
						if evalErr == nil {
							var rhs any
							rhs, evalErr = vm.evalExpr(stmt.Rhs[0], env)
							if evalErr == nil {
								current, _ := vm.get("x", env)
								var value any
								value, evalErr = vm.applyBinaryOp(compoundOperator(stmt.Tok), current, rhs)
								if evalErr == nil {
									vm.set("x", value, env)
								}
							}
						}
					}
					exec.finish()
					vm.endExecution(exec)
					value, _ := vm.get("x", env)
					results[mode] = fmt.Sprintf("%T:%v; %v; %d", value, value, evalErr, vm.LastStepCount())
				}
				if results[0] != results[1] {
					t.Fatalf("limit %d: optimized=%s, reference=%s", limit, results[0], results[1])
				}
			}
		})
	}
}
