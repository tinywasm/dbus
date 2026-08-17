// Portions Copyright (c) 2013, Georg Reinke, Google — BSD-2-Clause

package dbus

import (
	"bytes"
	"encoding/binary"
	"errors"
	"io"
	"reflect"
	"strconv"
	"strings"
	"unsafe"
)

var nativeEndian binary.ByteOrder

func detectEndianness() binary.ByteOrder {
	var x uint32 = 0x01020304
	if *(*byte)(unsafe.Pointer(&x)) == 0x01 {
		return binary.BigEndian
	}
	return binary.LittleEndian
}

func init() {
	nativeEndian = detectEndianness()
}

const protoVersion byte = 1

type Flags byte

const (
	FlagNoReplyExpected Flags = 1 << iota
	FlagNoAutoStart
	FlagAllowInteractiveAuthorization
)

type Type byte

const (
	TypeMethodCall Type = 1 + iota
	TypeMethodReply
	TypeError
	TypeSignal
	typeMax
)

func (t Type) String() string {
	switch t {
	case TypeMethodCall:
		return "method call"
	case TypeMethodReply:
		return "reply"
	case TypeError:
		return "error"
	case TypeSignal:
		return "signal"
	}
	return "invalid"
}

type HeaderField byte

const (
	FieldPath HeaderField = 1 + iota
	FieldInterface
	FieldMember
	FieldErrorName
	FieldReplySerial
	FieldDestination
	FieldSender
	FieldSignature
	FieldUnixFDs
	fieldMax
)

type InvalidMessageError string

func (e InvalidMessageError) Error() string {
	return "dbus: invalid message: " + string(e)
}

var fieldTypes = [fieldMax]reflect.Type{
	FieldPath:        objectPathType,
	FieldInterface:   stringType,
	FieldMember:      stringType,
	FieldErrorName:   stringType,
	FieldReplySerial: uint32Type,
	FieldDestination: stringType,
	FieldSender:      stringType,
	FieldSignature:   signatureType,
	FieldUnixFDs:     uint32Type,
}

var requiredFields = [typeMax][]HeaderField{
	TypeMethodCall:  {FieldPath, FieldMember},
	TypeMethodReply: {FieldReplySerial},
	TypeError:       {FieldErrorName, FieldReplySerial},
	TypeSignal:      {FieldPath, FieldInterface, FieldMember},
}

type Message struct {
	Type    Type
	Flags   Flags
	Headers map[HeaderField]Variant
	Body    []any

	serial uint32
}

func DecodeMessage(rd io.Reader) (msg *Message, err error) {
	var order binary.ByteOrder
	var hlength, length uint32
	var typ, flags, proto byte
	var headers []header

	b := make([]byte, 1)
	_, err = rd.Read(b)
	if err != nil {
		return nil, err
	}
	switch b[0] {
	case 'l':
		order = binary.LittleEndian
	case 'B':
		order = binary.BigEndian
	default:
		return nil, InvalidMessageError("invalid byte order")
	}

	dec := newDecoder(rd, order)
	dec.pos = 1

	msg = new(Message)
	vs, err := dec.Decode(Signature{"yyyuu"})
	if err != nil {
		return nil, err
	}
	if err = Store(vs, &typ, &flags, &proto, &length, &msg.serial); err != nil {
		return nil, err
	}
	_ = proto
	msg.Type = Type(typ)
	msg.Flags = Flags(flags)

	b = make([]byte, 4)
	_, err = io.ReadFull(rd, b)
	if err != nil {
		return nil, err
	}
	binary.Read(bytes.NewBuffer(b), order, &hlength)
	if hlength+length+16 > 1<<27 {
		return nil, InvalidMessageError("message is too long")
	}
	dec = newDecoder(io.MultiReader(bytes.NewBuffer(b), rd), order)
	dec.pos = 12
	vs, err = dec.Decode(Signature{"a(yv)"})
	if err != nil {
		return nil, err
	}
	if err = Store(vs, &headers); err != nil {
		return nil, err
	}

	msg.Headers = make(map[HeaderField]Variant)
	for _, v := range headers {
		msg.Headers[HeaderField(v.Field)] = v.Variant
	}

	dec.align(8)
	body := make([]byte, int(length))
	if length != 0 {
		_, err := io.ReadFull(rd, body)
		if err != nil {
			return nil, err
		}
	}

	if err = msg.IsValid(); err != nil {
		return nil, err
	}
	if sigVar, ok := msg.Headers[FieldSignature]; ok {
		if sig, ok := sigVar.value.(Signature); ok && sig.str != "" {
			buf := bytes.NewBuffer(body)
			dec = newDecoder(buf, order)
			vs, err := dec.Decode(sig)
			if err != nil {
				return nil, err
			}
			msg.Body = vs
		}
	}

	return msg, nil
}

type header struct {
	Field byte
	Variant
}

func (msg *Message) EncodeTo(out io.Writer, order binary.ByteOrder) (err error) {
	if err := msg.validateHeader(); err != nil {
		return err
	}
	var vs [7]any
	switch order {
	case binary.LittleEndian:
		vs[0] = byte('l')
	case binary.BigEndian:
		vs[0] = byte('B')
	default:
		return errors.New("dbus: invalid byte order")
	}
	body := new(bytes.Buffer)
	enc := newEncoder(body, order)
	if len(msg.Body) != 0 {
		err = enc.Encode(msg.Body...)
		if err != nil {
			return err
		}
	}
	vs[1] = byte(msg.Type)
	vs[2] = byte(msg.Flags)
	vs[3] = protoVersion
	vs[4] = uint32(len(body.Bytes()))
	vs[5] = msg.serial

	headers := make([]header, 0, len(msg.Headers))
	for k, v := range msg.Headers {
		headers = append(headers, header{byte(k), v})
	}
	vs[6] = headers

	var buf bytes.Buffer
	enc = newEncoder(&buf, order)
	err = enc.Encode(vs[:]...)
	if err != nil {
		return err
	}
	enc.align(8)
	body.WriteTo(&buf)
	if buf.Len() > 1<<27 {
		return InvalidMessageError("message is too long")
	}
	if _, err := buf.WriteTo(out); err != nil {
		return err
	}
	return nil
}

func (msg *Message) IsValid() error {
	var b bytes.Buffer
	return msg.EncodeTo(&b, nativeEndian)
}

func (msg *Message) validateHeader() error {
	if msg.Flags & ^(FlagNoAutoStart|FlagNoReplyExpected|FlagAllowInteractiveAuthorization) != 0 {
		return InvalidMessageError("invalid flags")
	}
	if msg.Type == 0 || msg.Type >= typeMax {
		return InvalidMessageError("invalid message type")
	}
	for k, v := range msg.Headers {
		if k == 0 || k >= fieldMax {
			return InvalidMessageError("invalid header")
		}
		if reflect.TypeOf(v.value) != fieldTypes[k] {
			return InvalidMessageError("invalid type of header field")
		}
	}
	for _, v := range requiredFields[msg.Type] {
		if _, ok := msg.Headers[v]; !ok {
			return InvalidMessageError("missing required header")
		}
	}
	if path, ok := msg.Headers[FieldPath]; ok {
		if !path.value.(ObjectPath).IsValid() {
			return InvalidMessageError("invalid path name")
		}
	}
	if iface, ok := msg.Headers[FieldInterface]; ok {
		if !isValidInterface(iface.value.(string)) {
			return InvalidMessageError("invalid interface name")
		}
	}
	if member, ok := msg.Headers[FieldMember]; ok {
		if !isValidMember(member.value.(string)) {
			return InvalidMessageError("invalid member name")
		}
	}
	if errname, ok := msg.Headers[FieldErrorName]; ok {
		if !isValidInterface(errname.value.(string)) {
			return InvalidMessageError("invalid error name")
		}
	}
	if len(msg.Body) != 0 {
		if _, ok := msg.Headers[FieldSignature]; !ok {
			return InvalidMessageError("missing signature")
		}
	}

	return nil
}

func (msg *Message) Serial() uint32 {
	return msg.serial
}

func (msg *Message) String() string {
	if err := msg.IsValid(); err != nil {
		return "<invalid>"
	}
	s := msg.Type.String()
	if v, ok := msg.Headers[FieldSender]; ok {
		s += " from " + v.value.(string)
	}
	if v, ok := msg.Headers[FieldDestination]; ok {
		s += " to " + v.value.(string)
	}
	s += " serial " + strconv.FormatUint(uint64(msg.serial), 10)
	if v, ok := msg.Headers[FieldReplySerial]; ok {
		s += " reply_serial " + strconv.FormatUint(uint64(v.value.(uint32)), 10)
	}
	if v, ok := msg.Headers[FieldUnixFDs]; ok {
		s += " unixfds " + strconv.FormatUint(uint64(v.value.(uint32)), 10)
	}
	if v, ok := msg.Headers[FieldPath]; ok {
		s += " path " + string(v.value.(ObjectPath))
	}
	if v, ok := msg.Headers[FieldInterface]; ok {
		s += " interface " + v.value.(string)
	}
	if v, ok := msg.Headers[FieldErrorName]; ok {
		s += " error " + v.value.(string)
	}
	if v, ok := msg.Headers[FieldMember]; ok {
		s += " member " + v.value.(string)
	}
	if len(msg.Body) != 0 {
		s += "\n"
	}
	for i, v := range msg.Body {
		s += "  " + MakeVariant(v).String()
		if i != len(msg.Body)-1 {
			s += "\n"
		}
	}
	return s
}

func isValidInterface(name string) bool {
	if len(name) == 0 || len(name) > 255 {
		return false
	}
	elements := strings.Split(name, ".")
	if len(elements) < 2 {
		return false
	}
	for _, elem := range elements {
		if len(elem) == 0 {
			return false
		}
		if elem[0] >= '0' && elem[0] <= '9' {
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

func isValidMember(name string) bool {
	if len(name) == 0 || len(name) > 255 {
		return false
	}
	if strings.Contains(name, ".") {
		return false
	}
	if name[0] >= '0' && name[0] <= '9' {
		return false
	}
	for _, b := range name {
		if !((b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z') || (b >= '0' && b <= '9') || b == '_') {
			return false
		}
	}
	return true
}
