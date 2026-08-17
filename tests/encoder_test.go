// Portions Copyright (c) 2013, Georg Reinke, Google — BSD-2-Clause

package tests

import (
	"bytes"
	"encoding/binary"
	"reflect"
	"testing"

	"github.com/tinywasm/dbus"
)

func TestEncodeArrayOfMaps(t *testing.T) {
	tests := []struct {
		name string
		vs   []any
	}{
		{
			"aligned at 8 at start of array",
			[]any{
				"12345",
				[]map[string]dbus.Variant{
					{
						"abcdefg": dbus.MakeVariant("foo"),
						"cdef":    dbus.MakeVariant(uint32(2)),
					},
				},
			},
		},
		{
			"not aligned at 8 for start of array",
			[]any{
				"1234567890",
				[]map[string]dbus.Variant{
					{
						"abcdefg": dbus.MakeVariant("foo"),
						"cdef":    dbus.MakeVariant(uint32(2)),
					},
				},
			},
		},
	}
	for _, order := range []binary.ByteOrder{binary.LittleEndian, binary.BigEndian} {
		for _, tt := range tests {
			buf := new(bytes.Buffer)
			enc := dbus.NewEncoder(buf, order)
			err := enc.EncodeValues(tt.vs...)
			if err != nil {
				t.Fatalf("%q: encode failed: %v", tt.name, err)
			}

			dec := dbus.NewDecoder(buf, order)
			v, err := dec.Decode(dbus.SignatureOf(tt.vs...))
			if err != nil {
				t.Errorf("%q: decode (%v) failed: %v", tt.name, order, err)
				continue
			}
			if !reflect.DeepEqual(v, tt.vs) {
				t.Errorf("%q: (%v) not equal: got '%v', want '%v'", tt.name, order, v, tt.vs)
				continue
			}
		}
	}
}

func TestEncodeMapStringInterface(t *testing.T) {
	val := map[string]any{"foo": "bar"}
	buf := new(bytes.Buffer)
	order := binary.LittleEndian
	enc := dbus.NewEncoder(buf, order)
	err := enc.EncodeValues(val)
	if err != nil {
		t.Fatal(err)
	}

	dec := dbus.NewDecoder(buf, order)
	v, err := dec.Decode(dbus.SignatureOf(val))
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]any{}
	err = dbus.Store(v, &out)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(out, val) {
		t.Errorf("not equal: got '%v', want '%v'", out, val)
	}
}

func TestEncodeSliceInterface(t *testing.T) {
	val := []any{"foo", "bar"}
	buf := new(bytes.Buffer)
	order := binary.LittleEndian
	enc := dbus.NewEncoder(buf, order)
	err := enc.EncodeValues(val)
	if err != nil {
		t.Fatal(err)
	}

	dec := dbus.NewDecoder(buf, order)
	v, err := dec.Decode(dbus.SignatureOf(val))
	if err != nil {
		t.Fatal(err)
	}
	out := []any{}
	err = dbus.Store(v, &out)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(out, val) {
		t.Errorf("not equal: got '%v', want '%v'", out, val)
	}
}

func TestEncodeInt(t *testing.T) {
	val := 10
	buf := new(bytes.Buffer)
	order := binary.LittleEndian
	enc := dbus.NewEncoder(buf, order)
	err := enc.EncodeValues(val)
	if err != nil {
		t.Fatal(err)
	}

	dec := dbus.NewDecoder(buf, order)
	v, err := dec.Decode(dbus.SignatureOf(val))
	if err != nil {
		t.Fatal(err)
	}
	var out int
	err = dbus.Store(v, &out)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(out, val) {
		t.Errorf("not equal: got '%v', want '%v'", out, val)
	}
}

func TestEncodeVariant(t *testing.T) {
	var res map[dbus.ObjectPath]map[string]map[string]dbus.Variant
	var src = map[dbus.ObjectPath]map[string]map[string]dbus.Variant{
		dbus.ObjectPath("/foo/bar"): {
			"foo": {
				"bar": dbus.MakeVariant(10),
				"baz": dbus.MakeVariant("20"),
			},
		},
	}
	buf := new(bytes.Buffer)
	order := binary.LittleEndian
	enc := dbus.NewEncoder(buf, order)
	err := enc.EncodeValues(src)
	if err != nil {
		t.Fatal(err)
	}

	dec := dbus.NewDecoder(buf, order)
	v, err := dec.Decode(dbus.SignatureOf(src))
	if err != nil {
		t.Fatal(err)
	}
	err = dbus.Store(v, &res)
	if err != nil {
		t.Fatal(err)
	}
	_ = res[dbus.ObjectPath("/foo/bar")]["foo"]["baz"].Value().(string)
}
