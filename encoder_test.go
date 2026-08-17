// Portions Copyright (c) 2013, Georg Reinke, Google — BSD-2-Clause

package dbus

import (
	"bytes"
	"encoding/binary"
	"reflect"
	"testing"
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
				[]map[string]Variant{
					{
						"abcdefg": MakeVariant("foo"),
						"cdef":    MakeVariant(uint32(2)),
					},
				},
			},
		},
		{
			"not aligned at 8 for start of array",
			[]any{
				"1234567890",
				[]map[string]Variant{
					{
						"abcdefg": MakeVariant("foo"),
						"cdef":    MakeVariant(uint32(2)),
					},
				},
			},
		},
	}
	for _, order := range []binary.ByteOrder{binary.LittleEndian, binary.BigEndian} {
		for _, tt := range tests {
			buf := new(bytes.Buffer)
			enc := newEncoder(buf, order)
			err := enc.Encode(tt.vs...)
			if err != nil {
				t.Fatalf("%q: encode failed: %v", tt.name, err)
			}

			dec := newDecoder(buf, order)
			v, err := dec.Decode(SignatureOf(tt.vs...))
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
	enc := newEncoder(buf, order)
	err := enc.Encode(val)
	if err != nil {
		t.Fatal(err)
	}

	dec := newDecoder(buf, order)
	v, err := dec.Decode(SignatureOf(val))
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]any{}
	err = Store(v, &out)
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
	enc := newEncoder(buf, order)
	err := enc.Encode(val)
	if err != nil {
		t.Fatal(err)
	}

	dec := newDecoder(buf, order)
	v, err := dec.Decode(SignatureOf(val))
	if err != nil {
		t.Fatal(err)
	}
	out := []any{}
	err = Store(v, &out)
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
	enc := newEncoder(buf, order)
	err := enc.Encode(val)
	if err != nil {
		t.Fatal(err)
	}

	dec := newDecoder(buf, order)
	v, err := dec.Decode(SignatureOf(val))
	if err != nil {
		t.Fatal(err)
	}
	var out int
	err = Store(v, &out)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(out, val) {
		t.Errorf("not equal: got '%v', want '%v'", out, val)
	}
}

func TestEncodeVariant(t *testing.T) {
	var res map[ObjectPath]map[string]map[string]Variant
	var src = map[ObjectPath]map[string]map[string]Variant{
		ObjectPath("/foo/bar"): {
			"foo": {
				"bar": MakeVariant(10),
				"baz": MakeVariant("20"),
			},
		},
	}
	buf := new(bytes.Buffer)
	order := binary.LittleEndian
	enc := newEncoder(buf, order)
	err := enc.Encode(src)
	if err != nil {
		t.Fatal(err)
	}

	dec := newDecoder(buf, order)
	v, err := dec.Decode(SignatureOf(src))
	if err != nil {
		t.Fatal(err)
	}
	err = Store(v, &res)
	if err != nil {
		t.Fatal(err)
	}
	_ = res[ObjectPath("/foo/bar")]["foo"]["baz"].Value().(string)
}
