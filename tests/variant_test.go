// Portions Copyright (c) 2013, Georg Reinke, Google — BSD-2-Clause

package tests

import (
	"testing"

	"github.com/tinywasm/dbus"
)

var variantFormatTests = []struct {
	v any
	s string
}{
	{int32(1), `1`},
	{"foo", `"foo"`},
	{dbus.ObjectPath("/org/foo"), `@o "/org/foo"`},
	{dbus.ParseSignatureMust("i"), `@g "i"`},
	{[]byte{}, `@ay []`},
	{[]int32{1, 2}, `[1, 2]`},
	{[]int64{1, 2}, `@ax [1, 2]`},
	{[][]int32{{3, 4}, {5, 6}}, `[[3, 4], [5, 6]]`},
	{[]dbus.Variant{dbus.MakeVariant(int32(1)), dbus.MakeVariant(1.0)}, `[<1>, <@d 1>]`},
	{map[string]int32{"one": 1, "two": 2}, `{"one": 1, "two": 2}`},
	{map[int32]dbus.ObjectPath{1: "/org/foo"}, `@a{io} {1: "/org/foo"}`},
	{map[string]dbus.Variant{}, `@a{sv} {}`},
}

func TestFormatVariant(t *testing.T) {
	for i, v := range variantFormatTests {
		if s := dbus.MakeVariant(v.v).String(); s != v.s {
			t.Errorf("test %d: got %q, wanted %q", i+1, s, v.s)
		}
	}
}

func TestVariantStore(t *testing.T) {
	str := "foo bar"
	v := dbus.MakeVariant(str)
	var result string
	err := dbus.Store([]any{v}, &result)
	if err != nil {
		t.Fatal(err)
	}
	if result != str {
		t.Fatalf("expected %s, got %s\n", str, result)
	}
}
