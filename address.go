// Portions Copyright (c) 2013, Georg Reinke, Google — BSD-2-Clause

package dbus

import (
	"errors"
	"net"
	"os"
	"strconv"
	"strings"
)

func unescapeBusAddressValue(s string) (string, error) {
	var buf strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] == '%' {
			if i+2 >= len(s) {
				return "", errors.New("dbus: invalid hex escape in address")
			}
			b, err := strconv.ParseUint(s[i+1:i+3], 16, 8)
			if err != nil {
				return "", errors.New("dbus: invalid hex escape in address")
			}
			buf.WriteByte(byte(b))
			i += 2
		} else {
			buf.WriteByte(s[i])
		}
	}
	return buf.String(), nil
}

func getKey(s, key string) string {
	for _, keyEqualsValue := range strings.Split(s, ",") {
		keyValue := strings.SplitN(keyEqualsValue, "=", 2)
		if len(keyValue) == 2 && keyValue[0] == key {
			val, err := unescapeBusAddressValue(keyValue[1])
			if err != nil {
				return ""
			}
			return val
		}
	}
	return ""
}

func dialUnixTransport(keys string) (*net.UnixConn, error) {
	abstract := getKey(keys, "abstract")
	path := getKey(keys, "path")
	switch {
	case abstract == "" && path == "":
		return nil, errors.New("dbus: invalid address (neither path nor abstract set)")
	case abstract != "" && path == "":
		return net.DialUnix("unix", nil, &net.UnixAddr{Name: "@" + abstract, Net: "unix"})
	case abstract == "" && path != "":
		return net.DialUnix("unix", nil, &net.UnixAddr{Name: path, Net: "unix"})
	default:
		return nil, errors.New("dbus: invalid address (both path and abstract set)")
	}
}

func dialAddress(address string) (*net.UnixConn, error) {
	addresses := strings.Split(address, ";")
	var lastErr error
	for _, v := range addresses {
		v = strings.TrimSpace(v)
		if v == "" {
			continue
		}
		i := strings.IndexRune(v, ':')
		if i == -1 {
			lastErr = errors.New("dbus: invalid bus address (no transport)")
			continue
		}
		transport := v[:i]
		if transport != "unix" {
			lastErr = errors.New("dbus: invalid bus address (invalid or unsupported transport)")
			continue
		}
		conn, err := dialUnixTransport(v[i+1:])
		if err == nil {
			return conn, nil
		}
		lastErr = err
	}
	if lastErr != nil {
		return nil, lastErr
	}
	return nil, errors.New("dbus: invalid bus address")
}

func getSessionBusAddress() (string, error) {
	addr := os.Getenv(envSessionBusAddress)
	if addr != "" {
		return addr, nil
	}
	runtimeDir := os.Getenv(envRuntimeDir)
	if runtimeDir != "" {
		return "unix:path=" + runtimeDir + "/bus", nil
	}
	return "", ErrNoSessionBus
}
