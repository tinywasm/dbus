// Portions Copyright (c) 2013, Georg Reinke, Google — BSD-2-Clause

package dbus

import (
	"bytes"
	"fmt"
	"reflect"
	"sort"
	"strconv"
)

// ObjectPath is a D-Bus object path, e.g. "/org/freedesktop/secrets".
type ObjectPath string

// IsValid returns whether the object path is valid according to the spec.
func (p ObjectPath) IsValid() bool {
	if len(p) == 0 {
		return false
	}
	if p[0] != '/' {
		return false
	}
	if p == "/" {
		return true
	}
	if p[len(p)-1] == '/' {
		return false
	}
	// Check element formatting
	elements := bytes.Split([]byte(p[1:]), []byte{'/'})
	for _, elem := range elements {
		if len(elem) == 0 {
			return false
		}
		for _, b := range elem {
			if !((b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z') || (b >= '0' && b <= '9') || b == '_') {
				return false
			}
		}
	}
	return true
}

// Variant represents the D-Bus variant type.
type Variant struct {
	sig   Signature
	value any
}

// MakeVariant converts the given value to a Variant.
func MakeVariant(v any) Variant {
	return MakeVariantWithSignature(v, SignatureOf(v))
}

// MakeVariantWithSignature converts the given value to a Variant with specified signature.
func MakeVariantWithSignature(v any, s Signature) Variant {
	return Variant{sig: s, value: v}
}

// Value returns the underlying value of v.
func (v Variant) Value() any {
	return v.value
}

// Signature returns the D-Bus signature of the underlying value of v.
func (v Variant) Signature() string {
	return v.sig.str
}

func (v Variant) format() (string, bool) {
	if v.sig.str == "" {
		return `"INVALID"`, true
	}
	switch v.sig.str[0] {
	case 'b', 'i':
		return fmt.Sprint(v.value), true
	case 'n', 'q', 'u', 'x', 't', 'd', 'h':
		return fmt.Sprint(v.value), false
	case 's':
		if str, ok := v.value.(string); ok {
			return strconv.Quote(str), true
		}
		return fmt.Sprint(v.value), true
	case 'o':
		if op, ok := v.value.(ObjectPath); ok {
			return strconv.Quote(string(op)), false
		}
		return strconv.Quote(fmt.Sprint(v.value)), false
	case 'g':
		if sig, ok := v.value.(Signature); ok {
			return strconv.Quote(sig.str), false
		}
		return strconv.Quote(fmt.Sprint(v.value)), false
	case 'v':
		if child, ok := v.value.(Variant); ok {
			s, unamb := child.format()
			if !unamb {
				return "<@" + child.sig.str + " " + s + ">", true
			}
			return "<" + s + ">", true
		}
	case 'y':
		if b, ok := v.value.(byte); ok {
			return fmt.Sprintf("%#x", b), false
		}
	}
	rv := reflect.ValueOf(v.value)
	if !rv.IsValid() {
		return `"INVALID"`, true
	}
	switch rv.Kind() {
	case reflect.Slice:
		if rv.Len() == 0 {
			return "[]", false
		}
		unamb := true
		buf := bytes.NewBuffer([]byte("["))
		for i := 0; i < rv.Len(); i++ {
			s, b := MakeVariant(rv.Index(i).Interface()).format()
			unamb = unamb && b
			buf.WriteString(s)
			if i != rv.Len()-1 {
				buf.WriteString(", ")
			}
		}
		buf.WriteByte(']')
		return buf.String(), unamb
	case reflect.Map:
		if rv.Len() == 0 {
			return "{}", false
		}
		unamb := true
		var buf bytes.Buffer
		kvs := make([]string, rv.Len())
		for i, k := range rv.MapKeys() {
			s, b := MakeVariant(k.Interface()).format()
			unamb = unamb && b
			buf.Reset()
			buf.WriteString(s)
			buf.WriteString(": ")
			s, b = MakeVariant(rv.MapIndex(k).Interface()).format()
			unamb = unamb && b
			buf.WriteString(s)
			kvs[i] = buf.String()
		}
		buf.Reset()
		buf.WriteByte('{')
		sort.Strings(kvs)
		for i, kv := range kvs {
			if i > 0 {
				buf.WriteString(", ")
			}
			buf.WriteString(kv)
		}
		buf.WriteByte('}')
		return buf.String(), unamb
	}
	return `"INVALID"`, true
}

func (v Variant) String() string {
	s, unamb := v.format()
	if !unamb {
		return "@" + v.sig.str + " " + s
	}
	return s
}
