// Portions Copyright (c) 2013, Georg Reinke, Google — BSD-2-Clause

package dbus

import (
	"encoding/binary"
	"io"
	"reflect"
)

type FormatError string

func (e FormatError) Error() string {
	return "dbus: wire format error: " + string(e)
}

type decoder struct {
	in    io.Reader
	order binary.ByteOrder
	pos   int
}

func newDecoder(in io.Reader, order binary.ByteOrder) *decoder {
	dec := new(decoder)
	dec.in = in
	dec.order = order
	return dec
}

func (dec *decoder) align(n int) {
	if dec.pos%n != 0 {
		newpos := (dec.pos + n - 1) & ^(n - 1)
		empty := make([]byte, newpos-dec.pos)
		if _, err := io.ReadFull(dec.in, empty); err != nil {
			panic(err)
		}
		dec.pos = newpos
	}
}

func (dec *decoder) binread(v any) {
	if err := binary.Read(dec.in, dec.order, v); err != nil {
		panic(err)
	}
}

func (dec *decoder) Decode(sig Signature) (vs []any, err error) {
	defer func() {
		if v := recover(); v != nil {
			var ok bool
			if err, ok = v.(error); ok {
				if err == io.EOF || err == io.ErrUnexpectedEOF {
					err = FormatError("unexpected EOF")
				}
			} else {
				panic(v)
			}
		}
	}()
	vs = make([]any, 0)
	s := sig.str
	for s != "" {
		err, rem := validSingle(s, &depthCounter{})
		if err != nil {
			return nil, err
		}
		v := dec.decode(s[:len(s)-len(rem)], 0)
		vs = append(vs, v)
		s = rem
	}
	return vs, nil
}

func (dec *decoder) decode(s string, depth int) any {
	dec.align(alignment(typeFor(s)))
	switch s[0] {
	case 'y':
		var b [1]byte
		if _, err := dec.in.Read(b[:]); err != nil {
			panic(err)
		}
		dec.pos++
		return b[0]
	case 'b':
		i := dec.decode("u", depth).(uint32)
		switch {
		case i == 0:
			return false
		case i == 1:
			return true
		default:
			panic(FormatError("invalid value for boolean"))
		}
	case 'n':
		var i int16
		dec.binread(&i)
		dec.pos += 2
		return i
	case 'i':
		var i int32
		dec.binread(&i)
		dec.pos += 4
		return i
	case 'x':
		var i int64
		dec.binread(&i)
		dec.pos += 8
		return i
	case 'q':
		var i uint16
		dec.binread(&i)
		dec.pos += 2
		return i
	case 'u':
		var i uint32
		dec.binread(&i)
		dec.pos += 4
		return i
	case 't':
		var i uint64
		dec.binread(&i)
		dec.pos += 8
		return i
	case 'd':
		var f float64
		dec.binread(&f)
		dec.pos += 8
		return f
	case 's':
		length := dec.decode("u", depth).(uint32)
		b := make([]byte, int(length)+1)
		if _, err := io.ReadFull(dec.in, b); err != nil {
			panic(err)
		}
		dec.pos += int(length) + 1
		return string(b[:len(b)-1])
	case 'o':
		return ObjectPath(dec.decode("s", depth).(string))
	case 'g':
		length := dec.decode("y", depth).(byte)
		b := make([]byte, int(length)+1)
		if _, err := io.ReadFull(dec.in, b); err != nil {
			panic(err)
		}
		dec.pos += int(length) + 1
		sig, err := ParseSignature(string(b[:len(b)-1]))
		if err != nil {
			panic(err)
		}
		return sig
	case 'v':
		if depth >= 64 {
			panic(FormatError("input exceeds container depth limit"))
		}
		var variant Variant
		sig := dec.decode("g", depth).(Signature)
		if len(sig.str) == 0 {
			panic(FormatError("variant signature is empty"))
		}
		err, rem := validSingle(sig.str, &depthCounter{})
		if err != nil {
			panic(err)
		}
		if rem != "" {
			panic(FormatError("variant signature has multiple types"))
		}
		variant.sig = sig
		variant.value = dec.decode(sig.str, depth+1)
		return variant
	case 'a':
		if len(s) > 1 && s[1] == '{' {
			ksig := s[2:3]
			vsig := s[3 : len(s)-1]
			v := reflect.MakeMap(reflect.MapOf(typeFor(ksig), typeFor(vsig)))
			if depth >= 63 {
				panic(FormatError("input exceeds container depth limit"))
			}
			length := dec.decode("u", depth).(uint32)
			dec.align(8)
			spos := dec.pos
			for dec.pos < spos+int(length) {
				dec.align(8)
				if !isKeyType(v.Type().Key()) {
					panic(InvalidTypeError{Type: v.Type()})
				}
				kv := dec.decode(ksig, depth+2)
				vv := dec.decode(vsig, depth+2)
				v.SetMapIndex(reflect.ValueOf(kv), reflect.ValueOf(vv))
			}
			return v.Interface()
		}
		if depth >= 64 {
			panic(FormatError("input exceeds container depth limit"))
		}
		sig := s[1:]
		length := dec.decode("u", depth).(uint32)
		var capacity int
		if sz := sigByteSize(sig); sz != 0 {
			capacity = int(length) / sz
		}
		v := reflect.MakeSlice(reflect.SliceOf(typeFor(sig)), 0, capacity)
		align := alignment(typeFor(s[1:]))
		if len(s) > 1 && s[1] == '(' {
			align = 8
		}
		dec.align(align)
		spos := dec.pos
		for dec.pos < spos+int(length) {
			ev := dec.decode(s[1:], depth+1)
			v = reflect.Append(v, reflect.ValueOf(ev))
		}
		return v.Interface()
	case '(':
		if depth >= 64 {
			panic(FormatError("input exceeds container depth limit"))
		}
		dec.align(8)
		v := make([]any, 0)
		s = s[1 : len(s)-1]
		for s != "" {
			err, rem := validSingle(s, &depthCounter{})
			if err != nil {
				panic(err)
			}
			ev := dec.decode(s[:len(s)-len(rem)], depth+1)
			v = append(v, ev)
			s = rem
		}
		return v
	default:
		panic(SignatureError{Sig: s})
	}
}

func sigByteSize(sig string) int {
	var total int
	for offset := 0; offset < len(sig); {
		switch sig[offset] {
		case 'y':
			total += 1
			offset += 1
		case 'n', 'q':
			total += 2
			offset += 1
		case 'b', 'i', 'u':
			total += 4
			offset += 1
		case 'x', 't', 'd':
			total += 8
			offset += 1
		case '(':
			i := 1
			depth := 1
			for i < len(sig[offset:]) && depth != 0 {
				if sig[offset+i] == '(' {
					depth++
				} else if sig[offset+i] == ')' {
					depth--
				}
				i++
			}
			s := sigByteSize(sig[offset+1 : offset+i-1])
			if s == 0 {
				return 0
			}
			total += s
			offset += i
		default:
			return 0
		}
	}
	return total
}
