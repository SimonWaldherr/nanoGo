package interp

import "testing"

func TestFlagSetParsingAndPointers(t *testing.T) {
	out := runAndCapture(t, `package main
import ("fmt"; cli "flag")
func main(){
 fs:=cli.NewFlagSet("test",cli.ContinueOnError)
 name:=fs.String("name","default","name")
 count:=fs.Int("n",1,"count")
 verbose:=fs.Bool("v",false,"verbose")
 scale:=fs.Float64("scale",1.0,"scale")
 var bound int;fs.IntVar(&bound,"bound",2,"bound")
 err:=fs.Parse([]string{"-name=nanoGo","-n","7","-v","-scale=1.5","-bound=9","file"})
 fmt.Println(err==nil,*name,*count,*verbose,*scale,bound,fs.Parsed(),fs.NFlag(),fs.NArg(),fs.Arg(0))
 fmt.Println(fs.Set("n","8")==nil,*count)
 fmt.Println(fs.Parse([]string{"-unknown"})!=nil)
 var zero cli.FlagSet;v:=zero.String("key","value","usage");fmt.Println(zero.Parse([]string{"-key=ok"})==nil,*v)
}`)
	want := "true nanoGo 7 true 1.5 9 true 5 1 file\ntrue 8\ntrue\ntrue ok\n"
	if out != want {
		t.Fatalf("got %q", out)
	}
}

func TestFlagGlobalArgsIsolation(t *testing.T) {
	vm, out := newTestVM()
	for _, value := range []string{"first", "second"} {
		out.Reset()
		vm.Args = []string{"guest", "-name", value, "tail"}
		if err := vm.Run(`package main
import("fmt";"flag")
var name=flag.String("name","default","name")
func main(){flag.Parse();fmt.Println(*name,flag.NArg(),flag.Arg(0))}`); err != nil {
			t.Fatal(err)
		}
		if out.String() != value+" 1 tail\n" {
			t.Fatalf("got %q", out.String())
		}
	}
}

func TestFlagPanicModeAndHelp(t *testing.T) {
	out := runAndCapture(t, `package main
import("fmt";"flag";"errors")
func main(){fs:=flag.NewFlagSet("help",flag.ContinueOnError);fmt.Println(errors.Is(fs.Parse([]string{"-h"}),flag.ErrHelp));p:=flag.NewFlagSet("panic",flag.PanicOnError);defer func(){fmt.Println(recover()!=nil)}();p.Parse([]string{"-bad"})}`)
	if out != "true\ntrue\n" {
		t.Fatalf("got %q", out)
	}
}
