// Portions Copyright (c) 2013, Georg Reinke, Google — BSD-2-Clause

package dbus

import (
	"bufio"
	"bytes"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
)

type authStatus byte

const (
	authOk authStatus = iota
	authContinue
	authError
)

type authState byte

const (
	waitingForData authState = iota
	waitingForOk
	waitingForReject
)

type authExternal struct {
	user string
}

func (a authExternal) FirstData() ([]byte, []byte, authStatus) {
	b := make([]byte, 2*len(a.user))
	hex.Encode(b, []byte(a.user))
	return []byte("EXTERNAL"), b, authOk
}

func (a authExternal) HandleData(b []byte) ([]byte, authStatus) {
	return nil, authError
}

func (c *Conn) auth() error {
	uid := strconv.Itoa(os.Geteuid())
	m := authExternal{user: uid}

	in := bufio.NewReader(c.transport)
	if _, err := c.transport.Write([]byte{0}); err != nil {
		return err
	}

	if err := authWriteLine(c.transport, []byte("AUTH")); err != nil {
		return err
	}

	s, err := authReadLine(in)
	if err != nil {
		return err
	}
	if len(s) < 2 || !bytes.Equal(s[0], []byte("REJECTED")) {
		return errors.New("dbus: authentication protocol error")
	}

	name, firstData, status := m.FirstData()
	var offered []string
	for _, v := range s[1:] {
		offered = append(offered, string(v))
	}

	found := false
	for _, v := range s[1:] {
		if bytes.Equal(v, name) {
			found = true
			break
		}
	}

	if !found {
		return fmt.Errorf("%w: offered mechanisms %v", ErrAuthFailed, offered)
	}

	err = authWriteLine(c.transport, []byte("AUTH"), name, firstData)
	if err != nil {
		return err
	}

	var initialState authState
	switch status {
	case authOk:
		initialState = waitingForOk
	case authContinue:
		initialState = waitingForData
	default:
		return errors.New("dbus: invalid authentication status")
	}

	err, ok := c.tryAuth(m, initialState, in)
	if err != nil {
		return err
	}
	if !ok {
		return ErrAuthFailed
	}

	err = authWriteLine(c.transport, []byte("BEGIN"))
	if err != nil {
		return err
	}

	return nil
}

func (c *Conn) tryAuth(m authExternal, state authState, in *bufio.Reader) (error, bool) {
	for {
		s, err := authReadLine(in)
		if err != nil {
			return err, false
		}
		if len(s) == 0 {
			continue
		}
		switch {
		case state == waitingForData && string(s[0]) == "DATA":
			if len(s) != 2 {
				err = authWriteLine(c.transport, []byte("ERROR"))
				if err != nil {
					return err, false
				}
				continue
			}
			data, status := m.HandleData(s[1])
			switch status {
			case authOk, authContinue:
				if len(data) != 0 {
					err = authWriteLine(c.transport, []byte("DATA"), data)
					if err != nil {
						return err, false
					}
				}
				if status == authOk {
					state = waitingForOk
				}
			case authError:
				err = authWriteLine(c.transport, []byte("ERROR"))
				if err != nil {
					return err, false
				}
			}
		case state == waitingForData && string(s[0]) == "REJECTED":
			return nil, false
		case state == waitingForData && string(s[0]) == "ERROR":
			err = authWriteLine(c.transport, []byte("CANCEL"))
			if err != nil {
				return err, false
			}
			state = waitingForReject
		case state == waitingForData && string(s[0]) == "OK":
			if len(s) != 2 {
				err = authWriteLine(c.transport, []byte("CANCEL"))
				if err != nil {
					return err, false
				}
				state = waitingForReject
			} else {
				c.uuid = string(s[1])
				return nil, true
			}
		case state == waitingForData:
			err = authWriteLine(c.transport, []byte("ERROR"))
			if err != nil {
				return err, false
			}
		case state == waitingForOk && string(s[0]) == "OK":
			if len(s) != 2 {
				err = authWriteLine(c.transport, []byte("CANCEL"))
				if err != nil {
					return err, false
				}
				state = waitingForReject
			} else {
				c.uuid = string(s[1])
				return nil, true
			}
		case state == waitingForOk && string(s[0]) == "DATA":
			err = authWriteLine(c.transport, []byte("DATA"))
			if err != nil {
				return err, false
			}
		case state == waitingForOk && string(s[0]) == "REJECTED":
			return nil, false
		case state == waitingForOk && string(s[0]) == "ERROR":
			err = authWriteLine(c.transport, []byte("CANCEL"))
			if err != nil {
				return err, false
			}
			state = waitingForReject
		case state == waitingForOk:
			err = authWriteLine(c.transport, []byte("ERROR"))
			if err != nil {
				return err, false
			}
		case state == waitingForReject && string(s[0]) == "REJECTED":
			return nil, false
		case state == waitingForReject:
			return errors.New("dbus: authentication protocol error"), false
		default:
			return errors.New("dbus: invalid auth state"), false
		}
	}
}

func authReadLine(in *bufio.Reader) ([][]byte, error) {
	data, err := in.ReadBytes('\n')
	if err != nil {
		return nil, err
	}
	data = bytes.TrimSuffix(data, []byte("\r\n"))
	return bytes.Split(data, []byte{' '}), nil
}

func authWriteLine(out io.Writer, data ...[]byte) error {
	buf := make([]byte, 0)
	for i, v := range data {
		buf = append(buf, v...)
		if i != len(data)-1 {
			buf = append(buf, ' ')
		}
	}
	buf = append(buf, '\r', '\n')
	n, err := out.Write(buf)
	if err != nil {
		return err
	}
	if n != len(buf) {
		return io.ErrUnexpectedEOF
	}
	return nil
}
