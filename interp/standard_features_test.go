package interp

import (
	"crypto/sha256"
	"math/rand"
	"sync"
	"testing"
)

func TestVirtualEnvironmentAPI(t *testing.T) {
	out := runAndCapture(t, `package main
import ("fmt"; env "os")
func main(){
 env.Clearenv()
 err:=env.Setenv("EMPTY", ""); fmt.Println(err==nil)
 value,ok:=env.LookupEnv("EMPTY");fmt.Println(value=="",ok)
 _,ok=env.LookupEnv("MISSING");fmt.Println(ok)
 env.Setenv("NAME","nanoGo");fmt.Println(env.ExpandEnv("Hello ${NAME}:$MISSING"))
 fmt.Println(env.Setenv("BAD=KEY","x")!=nil,env.Setenv("","x")!=nil)
 env.Unsetenv("EMPTY");_,ok=env.LookupEnv("EMPTY");fmt.Println(ok)
 env.Clearenv();fmt.Println(len(env.Environ()))
}`)
	want := "true\ntrue true\nfalse\nHello nanoGo:\ntrue true\nfalse\n0\n"
	if out != want {
		t.Fatalf("got %q, want %q", out, want)
	}
}

func TestRandIsolationAndSeed(t *testing.T) {
	vm1, _ := newTestVM()
	vm2, _ := newTestVM()
	p1, _ := vm1.ensureBuiltinPackage("math/rand")
	p2, _ := vm2.ensureBuiltinPackage("math/rand")
	p1.Funcs["Seed"].Native([]any{42})
	p2.Funcs["Seed"].Native([]any{42})
	want := rand.New(rand.NewSource(42))
	for i := 0; i < 10; i++ {
		expected := want.Intn(1000)
		for _, p := range []*Package{p1, p2} {
			got, err := p.Funcs["Intn"].Native([]any{1000})
			if err != nil || got != expected {
				t.Fatalf("got %v, %v; want %v", got, err, expected)
			}
		}
	}
	out := runAndCapture(t, `package main
import ("fmt"; random "math/rand")
func main(){defer func(){fmt.Println(recover()!=nil)}();random.Intn(0)}`)
	if out != "true\n" {
		t.Fatalf("got %q", out)
	}
}

func TestCryptoFacades(t *testing.T) {
	vm, _ := newTestVM()
	pkg, _ := vm.ensureBuiltinPackage("crypto/sha256")
	got, err := pkg.Funcs["Sum256"].Native([]any{byteSliceValue([]byte("abc"))})
	if err != nil {
		t.Fatal(err)
	}
	sum := got.(*SliceVal)
	want := sha256.Sum256([]byte("abc"))
	if !sum.Fixed || len(sum.Data) != len(want) {
		t.Fatal("wrong digest type/size")
	}
	for i, v := range want {
		if sum.Data[i] != int(v) {
			t.Fatalf("digest mismatch at %d", i)
		}
	}
	out := runAndCapture(t, `package main
import ("fmt"; random "crypto/rand"; hash "crypto/sha256")
func main(){p:=make([]byte,32);n,err:=random.Read(p);fmt.Println(n,err==nil,len(hash.Sum256(p)));n,err=random.Read(nil);fmt.Println(n,err==nil)}`)
	if out != "32 true 32\n0 true\n" {
		t.Fatalf("got %q", out)
	}
}

func TestSprintfWithoutHostHook(t *testing.T) {
	vm, out := newTestVM()
	delete(vm.natives, "__hostSprintf")
	if err := vm.Run(`package main
import format "fmt"
func main(){format.Println(format.Sprintf("%04d %s %.2f",7,"ok",1.25))}`); err != nil {
		t.Fatal(err)
	}
	if out.String() != "0007 ok 1.25\n" {
		t.Fatalf("got %q", out.String())
	}
}

func TestErrorfWrappingAndMultipleResults(t *testing.T) {
	out := runAndCapture(t, `package main
import ("fmt"; "errors")
var sentinel=errors.New("missing")
func find() (int,error){return 0,fmt.Errorf("lookup: %w",sentinel)}
func main(){n,err:=find();fmt.Println(n,err,errors.Is(err,sentinel),errors.Unwrap(err)==sentinel)}`)
	if out != "0 lookup: missing true true\n" {
		t.Fatalf("got %q", out)
	}
}

func TestVirtualEnvironmentCapabilitiesAndIsolation(t *testing.T) {
	vm, _ := newTestVM()
	vm.VFS.Setenv("PRIVATE", "kept")
	vm.Capabilities = Capabilities{}
	pkg, _ := vm.ensureBuiltinPackage("os")
	for name, args := range map[string][]any{
		"LookupEnv": {"PRIVATE"}, "ExpandEnv": {"$PRIVATE"},
		"Unsetenv": {"PRIVATE"}, "Clearenv": {},
	} {
		if _, err := pkg.Funcs[name].Native(args); err == nil {
			t.Fatalf("%s bypassed capabilities", name)
		}
	}
	if vm.VFS.Getenv("PRIVATE") != "kept" {
		t.Fatal("denied operation changed environment")
	}
	other := NewVFS()
	if _, found := other.LookupEnv("PRIVATE"); found {
		t.Fatal("environment leaked between VFS instances")
	}
}

func TestUnsetenvMissingKeyIsNoop(t *testing.T) {
	vm, _ := newTestVM()
	pkg, _ := vm.ensureBuiltinPackage("os")
	for _, key := range []string{"", "MISSING", "BAD=KEY"} {
		value, err := pkg.Funcs["Unsetenv"].Native([]any{key})
		if value != nil || err != nil {
			t.Fatalf("Unsetenv(%q) = %v, %v", key, value, err)
		}
	}
}

func TestRandConcurrentAccess(t *testing.T) {
	vm, _ := newTestVM()
	pkg, _ := vm.ensureBuiltinPackage("math/rand")
	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				pkg.Funcs["Seed"].Native([]any{j})
				pkg.Funcs["Intn"].Native([]any{10})
				pkg.Funcs["Float64"].Native(nil)
			}
		}()
	}
	wg.Wait()
}
