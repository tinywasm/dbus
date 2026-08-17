# tinywasm/dbus

A minimal, zero-dependency Go client for the Linux D-Bus session bus.

## Overview

`tinywasm/dbus` provides lightweight access to the Linux D-Bus session bus. It is specifically designed to support client operations needed for Secret Service interaction without bringing in large general-purpose D-Bus dependencies or external third-party packages.

## Scope Boundary

| In scope | Out of scope |
|---|---|
| Connect to the session bus over a Unix socket | System bus autolaunch, TCP transport |
| `EXTERNAL` SASL authentication | `DBUS_COOKIE_SHA1`, `ANONYMOUS` |
| Method calls with reply | Exporting objects, owning bus names |
| Receiving signals via `AddMatch` | Introspection, properties beyond `Get` |
| Basic type set (`byte`, `bool`, `uint32`, `string`, `ObjectPath`, `Variant`, arrays, structs, dict entries) | File descriptor passing, `int16`, `double`, `unix_fd` |

## Usage Example

```go
package main

import (
	"fmt"
	"log"

	"github.com/tinywasm/dbus"
)

func main() {
	conn, err := dbus.SessionBus()
	if err != nil {
		log.Fatalf("failed to connect to session bus: %v", err)
	}
	defer conn.Close()

	obj := conn.Object("org.freedesktop.DBus", "/org/freedesktop/DBus")
	reply := obj.Call("org.freedesktop.DBus.GetId")
	if reply.Err != nil {
		log.Fatalf("method call failed: %v", reply.Err)
	}

	var id string
	if err := reply.Store(&id); err != nil {
		log.Fatalf("failed to store reply: %v", err)
	}

	fmt.Println("Session Bus ID:", id)
}
```
