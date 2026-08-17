// Portions Copyright (c) 2013, Georg Reinke, Google — BSD-2-Clause

package tests

import (
	"testing"

	"github.com/tinywasm/dbus"
)

var sigTests = []struct {
	vs  []any
	sig string
}{
	{
		[]any{new(int32)},
		"i",
	},
	{
		[]any{new(string)},
		"s",
	},
	{
		[]any{new(dbus.Signature)},
		"g",
	},
	{
		[]any{new([]int16)},
		"an",
	},
	{
		[]any{new(int16), new(uint32)},
		"nu",
	},
	{
		[]any{new(map[byte]dbus.Variant)},
		"a{yv}",
	},
	{
		[]any{new(dbus.Variant), new([]map[int32]string)},
		"vaa{is}",
	},
}

func TestSig(t *testing.T) {
	for i, v := range sigTests {
		sig := dbus.SignatureOf(v.vs...)
		if sig.String() != v.sig {
			t.Errorf("test %d: got %q, expected %q", i+1, sig.String(), v.sig)
		}
	}
}
