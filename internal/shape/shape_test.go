package shape

import (
	"errors"
	"reflect"
	"testing"
)

func TestOf(t *testing.T) {
	cases := []struct {
		name string
		body string
		want Shape
	}{
		{"the doc comment's example", `{"id":"evt_1","items":[{"sku":"A"}]}`,
			Shape{"$": Object, "$.id": String, "$.items": Array, "$.items[]": Object, "$.items[].sku": String}},
		{"top-level array", `[1,2]`, Shape{"$": Array, "$[]": Number}},
		{"arrays of arrays", `{"a":[[true]]}`, Shape{"$": Object, "$.a": Array, "$.a[]": Array, "$.a[][]": Bool}},
		{"items disagree deeper down", `{"v":[{"a":1},{"a":"x"}]}`,
			Shape{"$": Object, "$.v": Array, "$.v[]": Object, "$.v[].a": Mixed}},
		{"a bare value", `null`, Shape{"$": Null}},
	}
	for _, c := range cases {
		got, err := Of([]byte(c.body))
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		if !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s:\n got  %v\n want %v", c.name, got, c.want)
		}
	}
}

func TestOfRejectsNonJSON(t *testing.T) {
	for _, body := range []string{"", "not json", `{"a":`} {
		if _, err := Of([]byte(body)); !errors.Is(err, ErrNotJSON) {
			t.Errorf("Of(%q) = %v, want ErrNotJSON", body, err)
		}
	}
}

func TestDiffIsSortedAndComplete(t *testing.T) {
	good := Shape{"$": Object, "$.b": Number, "$.a": String}
	bad := Shape{"$": Object, "$.b": String, "$.c": Bool}
	want := []Change{
		{Path: "$.a", Kind: Removed, Was: String},
		{Path: "$.b", Kind: Retyped, Was: Number, Now: String},
		{Path: "$.c", Kind: Added, Now: Bool},
	}
	for i := 0; i < 20; i++ { // map order is random; the result must not be
		got, err := Diff(good, bad)
		if err != nil || !reflect.DeepEqual(got, want) {
			t.Fatalf("Diff = %v, %v\nwant %v", got, err, want)
		}
	}
	if got, _ := Diff(good, good); len(got) != 0 {
		t.Fatalf("identical shapes: %v", got)
	}
}
