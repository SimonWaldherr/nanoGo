package interp

import (
	"reflect"
	"testing"
)

func TestStructTagLookupGuest(t *testing.T) {
	out := runAndCapture(t, "package main\nimport (\"fmt\"; \"reflect\"; \"encoding/json\")\ntype Item struct {\n"+
		"Name string `json:\"-,omitempty\" empty:\"\" pattern:\"a\\\\b\"`\n"+
		"Hidden string `json:\"-\"`\n}\n"+`
func main() {
 item := Item{Name: "visible", Hidden: "secret"}
 tag := reflect.TypeOf(item).Field(0).Tag
 value, found := tag.Lookup("empty")
 fmt.Println(value == "", found)
 value, found = tag.Lookup("missing")
 fmt.Println(value == "", found)
 value, found = tag.Lookup("pattern")
 fmt.Println(value, found)
 _, found = tag.Lookup("json")
 fmt.Println(found)
 data, err := json.Marshal(item)
 fmt.Println(string(data), err == nil)
}`)
	want := "true true\ntrue false\na\\b true\ntrue\n{\"-\":\"visible\"} true\n"
	if out != want {
		t.Fatalf("got %q, want %q", out, want)
	}
}

func TestJSONFieldTagOptions(t *testing.T) {
	for _, tc := range []struct {
		tag, name  string
		omit, skip bool
	}{
		{``, "Field", false, false},
		{`json:"-"`, "", false, true},
		{`json:"-,"`, "-", false, false},
		{`json:"-,omitempty"`, "-", true, false},
		{`json:",omitempty"`, "Field", true, false},
		{`json:"name,unknown,,omitempty,unknown"`, "name", true, false},
		{`json:"name,notomitempty,omitemptyextra"`, "name", false, false},
	} {
		name, omit, skip := jsonFieldOptions("Field", tc.tag)
		if name != tc.name || omit != tc.omit || skip != tc.skip {
			t.Errorf("%s: got (%q, %v, %v), want (%q, %v, %v)", tc.tag, name, omit, skip, tc.name, tc.omit, tc.skip)
		}
	}
}

func TestStructTagLookupMatchesGo(t *testing.T) {
	for _, tag := range []string{
		`json:"name,omitempty" validate:"required"`, `empty:""`,
		`escaped:"a\"b\\c" json:"ok"`, `broken:"\q" json:"ok"`,
		`json:"unfinished\`, `json:"unfinished`, `bad key:"x" json:"ok"`,
		"bad\tkey:\"x\" json:\"ok\"", `json:"first" json:"second"`,
		` json:"ok"`, `json:""`, "", `:"bad"`,
	} {
		for _, key := range []string{"json", "validate", "empty", "escaped", "missing", ""} {
			want, wantOK := reflect.StructTag(tag).Lookup(key)
			got, gotOK := structTagValue(tag, key)
			if got != want || gotOK != wantOK {
				t.Errorf("Lookup(%q, %q) = (%q, %v), want (%q, %v)", tag, key, got, gotOK, want, wantOK)
			}
		}
	}
}

func FuzzStructTagLookup(f *testing.F) {
	f.Add(`json:"name"`, "json")
	f.Add(`json:"unfinished\`, "json")
	f.Fuzz(func(t *testing.T, tag, key string) {
		want, wantOK := reflect.StructTag(tag).Lookup(key)
		got, gotOK := structTagValue(tag, key)
		if got != want || gotOK != wantOK {
			t.Fatalf("got (%q, %v), want (%q, %v)", got, gotOK, want, wantOK)
		}
	})
}

var tagBenchmarkName string
var tagBenchmarkFlag bool

func BenchmarkStructTagOptions(b *testing.B) {
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		tagBenchmarkName, tagBenchmarkFlag, _ = jsonFieldOptions("Name", `validate:"required,min=3" json:"name,omitempty"`)
	}
}

func BenchmarkStructTagLookupEscapedPrefix(b *testing.B) {
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		tagBenchmarkName, tagBenchmarkFlag = structTagValue(`pattern:"a\\b" json:"name"`, "json")
	}
}
