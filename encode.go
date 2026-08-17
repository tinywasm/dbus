// Portions Copyright (c) 2013, Georg Reinke, Google — BSD-2-Clause

package dbus

import (
	"bytes"
	"encoding/binary"
	"io"
	"reflect"
	"strings"
	"unicode/utf8"
)

func alignment(t reflect.Type) int {
	if t == nil {
		return 1
	}
	if t == variantType {
		return 1
	}
	if t == signatureType {
		return 1
	}
	switch t.Kind() {
	case reflect.Uint8:
		return 1
	case reflect.Int16, reflect.Uint16:
		return 2
	case reflect.Bool, reflect.Int, reflect.Int32, reflect.Uint, reflect.Uint32, reflect.String:
		return 4
	case reflect.Int64, reflect.Uint64, reflect.Float64, reflect.Struct:
		return 8
	case reflect.Slice, reflect.Array:
		return 4
	case reflect.Map:
		return 4
	case reflect.Interface:
		return 1
	case reflect.Ptr:
		return alignment(t.Elem())
	}
	return 1
}

type encoder struct {
	out   io.Writer
	order binary.ByteOrder
	pos   int
}

func newEncoder(out io.Writer, order binary.ByteOrder) *encoder {
	return newEncoderAtOffset(out, 0, order)
}

func newEncoderAtOffset(out io.Writer, offset int, order binary.ByteOrder) *encoder {
	enc := new(encoder)
	enc.out = out
	enc.order = order
	enc.pos = offset
	return enc
}

func (enc *encoder) align(n int) {
	pad := enc.padding(0, n)
	if pad > 0 {
		empty := make([]byte, pad)
		if _, err := enc.out.Write(empty); err != nil {
			panic(err)
		}
		enc.pos += pad
	}
}

func (enc *encoder) padding(offset, algn int) int {
	abs := enc.pos + offset
	if abs%algn != 0 {
		newabs := (abs + algn - 1) & ^(algn - 1)
		return newabs - abs
	}
	return 0
}

func (enc *encoder) binwrite(v any) {
	if err := binary.Write(enc.out, enc.order, v); err != nil {
		panic(err)
	}
}

func (enc *encoder) Encode(vs ...any) (err error) {
	defer func() {
		if r := recover(); r != nil {
			if e, ok := r.(error); ok {
				err = e
			} else {
				panic(r)
			}
		}
	}()
	for _, v := range vs {
		if v == nil {
			panic(ErrUnsupportedType)
		}
		enc.encode(reflect.ValueOf(v), 0)
	}
	return nil
}

func (enc *encoder) encode(v reflect.Value, depth int) {
	if depth > 64 {
		panic(FormatError("input exceeds depth limitation"))
	}
	enc.align(alignment(v.Type()))
	switch v.Kind() {
	case reflect.Uint8:
		var b [1]byte
		b[0] = byte(v.Uint())
		if _, err := enc.out.Write(b[:]); err != nil {
			panic(err)
		}
		enc.pos++
	case reflect.Bool:
		if v.Bool() {
			enc.encode(reflect.ValueOf(uint32(1)), depth)
		} else {
			enc.encode(reflect.ValueOf(uint32(0)), depth)
		}
	case reflect.Int16:
		enc.binwrite(int16(v.Int()))
		enc.pos += 2
	case reflect.Uint16:
		enc.binwrite(uint16(v.Uint()))
		enc.pos += 2
	case reflect.Int, reflect.Int32:
		enc.binwrite(int32(v.Int()))
		enc.pos += 4
	case reflect.Uint, reflect.Uint32:
		enc.binwrite(uint32(v.Uint()))
		enc.pos += 4
	case reflect.Int64:
		enc.binwrite(v.Int())
		enc.pos += 8
	case reflect.Uint64:
		enc.binwrite(v.Uint())
		enc.pos += 8
	case reflect.Float64:
		enc.binwrite(v.Float())
		enc.pos += 8
	case reflect.String:
		str := v.String()
		if !utf8.ValidString(str) {
			panic(FormatError("input has a not-utf8 char in string"))
		}
		if strings.IndexByte(str, byte(0)) != -1 {
			panic(FormatError("input has a null char('\\000') in string"))
		}
		if v.Type() == objectPathType {
			if !ObjectPath(str).IsValid() {
				panic(FormatError("invalid object path"))
			}
		}
		enc.encode(reflect.ValueOf(uint32(len(str))), depth)
		b := make([]byte, v.Len()+1)
		copy(b, str)
		b[len(b)-1] = 0
		n, err := enc.out.Write(b)
		if err != nil {
			panic(err)
		}
		enc.pos += n
	case reflect.Ptr:
		if v.IsNil() {
			panic(ErrUnsupportedType)
		}
		enc.encode(v.Elem(), depth)
	case reflect.Slice, reflect.Array:
		n := enc.padding(0, 4) + 4
		offset := enc.pos + n + enc.padding(n, alignment(v.Type().Elem()))

		var buf bytes.Buffer
		bufenc := newEncoderAtOffset(&buf, offset, enc.order)

		for i := 0; i < v.Len(); i++ {
			bufenc.encode(v.Index(i), depth+1)
		}

		if buf.Len() > 1<<26 {
			panic(FormatError("input exceeds array size limitation"))
		}

		enc.encode(reflect.ValueOf(uint32(buf.Len())), depth)
		length := buf.Len()
		enc.align(alignment(v.Type().Elem()))
		if _, err := buf.WriteTo(enc.out); err != nil {
			panic(err)
		}
		enc.pos += length
	case reflect.Struct:
		switch t := v.Type(); t {
		case signatureType:
			str := v.Field(0)
			enc.encode(reflect.ValueOf(byte(str.Len())), depth)
			b := make([]byte, str.Len()+1)
			copy(b, str.String())
			b[len(b)-1] = 0
			n, err := enc.out.Write(b)
			if err != nil {
				panic(err)
			}
			enc.pos += n
		case variantType:
			variant := v.Interface().(Variant)
			enc.encode(reflect.ValueOf(variant.sig), depth+1)
			enc.encode(reflect.ValueOf(variant.value), depth+1)
		default:
			for i := 0; i < v.Type().NumField(); i++ {
				field := t.Field(i)
				if field.PkgPath == "" && field.Tag.Get("dbus") != "-" {
					enc.encode(v.Field(i), depth+1)
				}
			}
		}
	case reflect.Map:
		if !isKeyType(v.Type().Key()) {
			panic(InvalidTypeError{Type: v.Type()})
		}
		keys := v.MapKeys()
		n := enc.padding(0, 4) + 4
		offset := enc.pos + n + enc.padding(n, 8)

		var buf bytes.Buffer
		bufenc := newEncoderAtOffset(&buf, offset, enc.order)
		for _, k := range keys {
			bufenc.align(8)
			bufenc.encode(k, depth+2)
			bufenc.encode(v.MapIndex(k), depth+2)
		}
		enc.encode(reflect.ValueOf(uint32(buf.Len())), depth)
		length := buf.Len()
		enc.align(8)
		if _, err := buf.WriteTo(enc.out); err != nil {
			panic(err)
		}
		enc.pos += length
	case reflect.Interface:
		if v.IsNil() {
			panic(ErrUnsupportedType)
		}
		enc.encode(reflect.ValueOf(MakeVariant(v.Interface())), depth)
	default:
		panic(InvalidTypeError{Type: v.Type()})
	}
}
