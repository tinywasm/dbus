// Portions Copyright (c) 2013, Georg Reinke, Google — BSD-2-Clause

package dbus

import (
	"fmt"
	"reflect"
	"strings"
)

var (
	byteType       = reflect.TypeOf(byte(0))
	boolType       = reflect.TypeOf(false)
	int16Type      = reflect.TypeOf(int16(0))
	uint16Type     = reflect.TypeOf(uint16(0))
	int32Type      = reflect.TypeOf(int32(0))
	uint32Type     = reflect.TypeOf(uint32(0))
	int64Type      = reflect.TypeOf(int64(0))
	uint64Type     = reflect.TypeOf(uint64(0))
	float64Type    = reflect.TypeOf(float64(0))
	stringType     = reflect.TypeOf("")
	signatureType  = reflect.TypeOf(Signature{})
	objectPathType = reflect.TypeOf(ObjectPath(""))
	variantType    = reflect.TypeOf(Variant{})
	interfacesType = reflect.TypeOf([]any{})
)

var sigToType = map[byte]reflect.Type{
	'y': byteType,
	'b': boolType,
	'n': int16Type,
	'q': uint16Type,
	'i': int32Type,
	'u': uint32Type,
	'x': int64Type,
	't': uint64Type,
	'd': float64Type,
	's': stringType,
	'g': signatureType,
	'o': objectPathType,
	'v': variantType,
}

// Signature represents a correct type signature as specified by the D-Bus
// specification. The zero value represents the empty signature, "".
type Signature struct {
	str string
}

// SignatureOf returns the concatenation of all the signatures of the given
// values. It panics if one of them is not representable in D-Bus.
func SignatureOf(vs ...any) Signature {
	var s string
	for _, v := range vs {
		if v == nil {
			panic(InvalidTypeError{Type: reflect.TypeOf(v)})
		}
		s += getSignature(reflect.TypeOf(v), &depthCounter{})
	}
	return Signature{s}
}

// SignatureOfType returns the signature of the given type. It panics if the
// type is not representable in D-Bus.
func SignatureOfType(t reflect.Type) Signature {
	return Signature{getSignature(t, &depthCounter{})}
}

func isKeyType(t reflect.Type) bool {
	if t == nil {
		return false
	}
	switch t.Kind() {
	case reflect.Uint8, reflect.Bool, reflect.Int16, reflect.Uint16,
		reflect.Int, reflect.Int32, reflect.Uint, reflect.Uint32,
		reflect.Int64, reflect.Uint64, reflect.Float64, reflect.String:
		return true
	}
	return false
}

func getSignature(t reflect.Type, depth *depthCounter) (sig string) {
	if !depth.Valid() {
		panic("container nesting too deep")
	}
	defer func() {
		if len(sig) > 255 {
			panic("signature exceeds the length limitation")
		}
	}()
	if t == nil {
		panic(InvalidTypeError{Type: t})
	}
	switch t.Kind() {
	case reflect.Uint8:
		return "y"
	case reflect.Bool:
		return "b"
	case reflect.Int16:
		return "n"
	case reflect.Uint16:
		return "q"
	case reflect.Int, reflect.Int32:
		return "i"
	case reflect.Uint, reflect.Uint32:
		return "u"
	case reflect.Int64:
		return "x"
	case reflect.Uint64:
		return "t"
	case reflect.Float64:
		return "d"
	case reflect.Ptr:
		return getSignature(t.Elem(), depth)
	case reflect.String:
		if t == objectPathType {
			return "o"
		}
		return "s"
	case reflect.Struct:
		if t == variantType {
			return "v"
		} else if t == signatureType {
			return "g"
		}
		var s string
		for i := 0; i < t.NumField(); i++ {
			field := t.Field(i)
			if field.PkgPath == "" && field.Tag.Get("dbus") != "-" {
				s += getSignature(t.Field(i).Type, depth.EnterStruct())
			}
		}
		if len(s) == 0 {
			panic(InvalidTypeError{Type: t})
		}
		return "(" + s + ")"
	case reflect.Array, reflect.Slice:
		return "a" + getSignature(t.Elem(), depth.EnterArray())
	case reflect.Map:
		if !isKeyType(t.Key()) {
			panic(InvalidTypeError{Type: t})
		}
		return "a{" + getSignature(t.Key(), depth.EnterArray().EnterDictEntry()) + getSignature(t.Elem(), depth.EnterArray().EnterDictEntry()) + "}"
	case reflect.Interface:
		return "v"
	}
	panic(InvalidTypeError{Type: t})
}

// ParseSignature returns the signature represented by this string, or a
// SignatureError if the string is not a valid signature.
func ParseSignature(s string) (sig Signature, err error) {
	if len(s) == 0 {
		return
	}
	if len(s) > 255 {
		return Signature{""}, SignatureError{s, "too long"}
	}
	sig.str = s
	for err == nil && len(s) != 0 {
		err, s = validSingle(s, &depthCounter{})
	}
	if err != nil {
		sig = Signature{""}
	}

	return
}

// ParseSignatureMust behaves like ParseSignature, except that it panics if s
// is not valid.
func ParseSignatureMust(s string) Signature {
	sig, err := ParseSignature(s)
	if err != nil {
		panic(err)
	}
	return sig
}

// Empty returns whether the signature is the empty signature.
func (s Signature) Empty() bool {
	return s.str == ""
}

// Single returns whether the signature represents a single, complete type.
func (s Signature) Single() bool {
	err, r := validSingle(s.str, &depthCounter{})
	return err == nil && r == ""
}

// String returns the signature's string representation.
func (s Signature) String() string {
	return s.str
}

// A SignatureError indicates that a signature passed to a function or received
// on a connection is not a valid signature.
type SignatureError struct {
	Sig    string
	Reason string
}

func (e SignatureError) Error() string {
	return fmt.Sprintf("dbus: invalid signature: %q (%s)", e.Sig, e.Reason)
}

type depthCounter struct {
	arrayDepth, structDepth, dictEntryDepth int
}

func (cnt *depthCounter) Valid() bool {
	return cnt.arrayDepth <= 32 && cnt.structDepth <= 32 && cnt.dictEntryDepth <= 32
}

func (cnt depthCounter) EnterArray() *depthCounter {
	cnt.arrayDepth++
	return &cnt
}

func (cnt depthCounter) EnterStruct() *depthCounter {
	cnt.structDepth++
	return &cnt
}

func (cnt depthCounter) EnterDictEntry() *depthCounter {
	cnt.dictEntryDepth++
	return &cnt
}

func validSingle(s string, depth *depthCounter) (err error, rem string) {
	if s == "" {
		return SignatureError{Sig: s, Reason: "empty signature"}, ""
	}
	if !depth.Valid() {
		return SignatureError{Sig: s, Reason: "container nesting too deep"}, ""
	}
	switch s[0] {
	case 'y', 'b', 'n', 'q', 'i', 'u', 'x', 't', 'd', 's', 'g', 'o', 'v':
		return nil, s[1:]
	case 'a':
		if len(s) > 1 && s[1] == '{' {
			i := findMatching(s[1:], '{', '}')
			if i == -1 {
				return SignatureError{Sig: s, Reason: "unmatched '{'"}, ""
			}
			i++
			rem = s[i+1:]
			s = s[2:i]
			if err, _ = validSingle(s[:1], depth.EnterArray().EnterDictEntry()); err != nil {
				return err, ""
			}
			err, nr := validSingle(s[1:], depth.EnterArray().EnterDictEntry())
			if err != nil {
				return err, ""
			}
			if nr != "" {
				return SignatureError{Sig: s, Reason: "too many types in dict"}, ""
			}
			return nil, rem
		}
		return validSingle(s[1:], depth.EnterArray())
	case '(':
		i := findMatching(s, '(', ')')
		if i == -1 {
			return SignatureError{Sig: s, Reason: "unmatched ')'"}, ""
		}
		rem = s[i+1:]
		s = s[1:i]
		for err == nil && s != "" {
			err, s = validSingle(s, depth.EnterStruct())
		}
		if err != nil {
			rem = ""
		}
		return
	}
	return SignatureError{Sig: s, Reason: "invalid type character"}, ""
}

func findMatching(s string, left, right rune) int {
	n := 0
	for i, v := range s {
		if v == left {
			n++
		} else if v == right {
			n--
		}
		if n == 0 {
			return i
		}
	}
	return -1
}

func typeFor(s string) (t reflect.Type) {
	err, _ := validSingle(s, &depthCounter{})
	if err != nil {
		panic(err)
	}

	if t, ok := sigToType[s[0]]; ok {
		return t
	}
	switch s[0] {
	case 'a':
		if s[1] == '{' {
			i := strings.LastIndex(s, "}")
			t = reflect.MapOf(sigToType[s[2]], typeFor(s[3:i]))
		} else {
			t = reflect.SliceOf(typeFor(s[1:]))
		}
	case '(':
		t = interfacesType
	}
	return
}
