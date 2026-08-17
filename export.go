// Portions Copyright (c) 2013, Georg Reinke, Google — BSD-2-Clause

package dbus

import (
	"encoding/binary"
	"io"
)

func NewEncoder(out io.Writer, order binary.ByteOrder) *encoder {
	return newEncoder(out, order)
}

func (enc *encoder) EncodeValues(vs ...any) error {
	return enc.Encode(vs...)
}

func NewDecoder(in io.Reader, order binary.ByteOrder) *decoder {
	return newDecoder(in, order)
}

func SigByteSize(sig string) int {
	return sigByteSize(sig)
}

func ValidateHeader(msg *Message) error {
	return msg.validateHeader()
}

func DialAddress(addr string) (io.Closer, error) {
	return dialAddress(addr)
}

func DialUnixTransport(keys string) (io.Closer, error) {
	return dialUnixTransport(keys)
}

func GetSessionBusAddress() (string, error) {
	return getSessionBusAddress()
}
