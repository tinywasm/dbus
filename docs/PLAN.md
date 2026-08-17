---
PLAN: "feat: minimal D-Bus session bus client with no external dependencies"
TAG: v0.1.0
EXECUTOR: jules
REVIEWER: none
---

> This plan is dispatched via the CodeJob workflow. See skill: agents-workflow.

# Plan — `tinywasm/dbus`, a minimal D-Bus session bus client

## 1. Why this module exists

`tinywasm/keyring` stores secrets in the Linux Secret Service, which is only
reachable over D-Bus. The only mature Go client, `github.com/godbus/dbus/v5`, is
a full implementation of the specification: signal matching, introspection,
name ownership, object export, reflection-driven marshalling for every D-Bus
type. `keyring` uses a fraction of it, and pulling the whole thing in makes a
zero-third-party-dependency ecosystem impossible.

This module implements **only what a client of the session bus needs to call
methods and receive signals**. It is not a general D-Bus library and does not
try to become one. It is deliberately small enough to audit.

**Scope boundary — do not exceed it:**

| In scope | Out of scope |
|---|---|
| Connect to the session bus over a Unix socket | System bus autolaunch, TCP transport |
| `EXTERNAL` SASL authentication | `DBUS_COOKIE_SHA1`, `ANONYMOUS` |
| Method calls with reply | Exporting objects, owning bus names |
| Receiving signals via `AddMatch` | Introspection, properties beyond `Get` |
| The type set in §4 | File descriptor passing, `int16`, `double`, `unix_fd` |

## 2. Ecosystem rules that apply here

- **This module is backend-only, and carries no build tags.** It talks to a Unix
  socket, so it can never do anything useful in a browser — but Go only compiles
  what something imports, and no browser build imports this. A `//go:build
  !wasm` tag would buy a compile-time error instead of a runtime one for a
  mistake nobody is going to make, at the cost of a constraint on every file.
  Do not add one.
- **The stdlib is legitimate and expected here** — `net`, `os`,
  `encoding/binary`, `errors`, `strconv`, `sync`. Do **not** replace them with
  `tinywasm/*` packages; that rule exists for WASM-bound code and does not apply
  to this repo.
- **Zero third-party dependencies.** `go.mod` must contain no `require` block
  other than the Go version. Not even `tinywasm/*` — nothing is needed.
- No hardcoded strings in logic: every interface name, path and member name is a
  typed constant (§6).
- `cmd/` is not part of this module. There is no CLI.

## 3. Public API — implement exactly this surface

```go
package dbus

// ObjectPath is a D-Bus object path, e.g. "/org/freedesktop/secrets".
type ObjectPath string

// Variant is a value tagged with its D-Bus signature.
type Variant struct {
    sig   string
    value any
}

func MakeVariant(v any) Variant
func (v Variant) Value() any
func (v Variant) Signature() string

// Conn is a connection to a message bus.
type Conn struct { /* unexported */ }

// SessionBus dials the address in $DBUS_SESSION_BUS_ADDRESS, performs the
// EXTERNAL handshake and calls Hello. The returned Conn is safe for concurrent
// use.
func SessionBus() (*Conn, error)

// Close terminates the connection and stops the read loop.
func (c *Conn) Close() error

// Object returns a handle to a remote object.
func (c *Conn) Object(dest string, path ObjectPath) *Object

// AddMatch installs a match rule on the bus so matching signals are delivered.
func (c *Conn) AddMatch(rule string) error
func (c *Conn) RemoveMatch(rule string) error

// Signals registers ch to receive every signal delivered to this connection.
// The caller must drain it; a full channel drops signals rather than blocking
// the read loop.
func (c *Conn) Signals(ch chan<- *Signal)

// Signal is a received signal message.
type Signal struct {
    Sender string
    Path   ObjectPath
    Name   string // "interface.Member"
    Body   []any
}

// Object is a remote object on the bus.
type Object struct { /* unexported */ }

// Call invokes method ("interface.Member") with args and waits for the reply.
func (o *Object) Call(method string, args ...any) *Reply

// GetProperty reads a property via org.freedesktop.DBus.Properties.Get.
func (o *Object) GetProperty(iface, name string) (Variant, error)

// Reply is the result of a method call.
type Reply struct {
    Body []any
    Err  error
}

// Store decodes the reply body into the pointers in dest, positionally.
func (r *Reply) Store(dest ...any) error
```

Design notes the executor must honour:

- `Call` **always waits for a reply** (`NO_REPLY_EXPECTED` is never set). A
  `METHOD_RETURN` fills `Body`; an `ERROR` message fills `Err` with an `Error`
  value carrying the D-Bus error name.
- `Store` is positional and type-strict. Mismatched arity or type is an error,
  never a silent zero value.
- No reflection-based marshalling of arbitrary structs. The encoder accepts the
  concrete types in §4 and returns `ErrUnsupportedType` for anything else.

## 4. The wire format — implement exactly this type set

D-Bus is a binary protocol. All integers are little-endian (the connection
always announces `'l'`). **Every type is aligned to its own boundary, and
padding bytes must be zero.**

| D-Bus type | Signature | Go type | Alignment | Encoding |
|---|---|---|---|---|
| BYTE | `y` | `byte` | 1 | one byte |
| BOOLEAN | `b` | `bool` | 4 | uint32, 0 or 1 |
| UINT32 | `u` | `uint32` | 4 | little-endian |
| STRING | `s` | `string` | 4 | uint32 length (no NUL) + bytes + NUL |
| OBJECT_PATH | `o` | `ObjectPath` | 4 | same as STRING |
| SIGNATURE | `g` | `string` | 1 | **byte** length + bytes + NUL |
| ARRAY | `a<T>` | `[]T` | 4 | uint32 **byte** length, then padding to T's alignment, then elements |
| STRUCT | `(...)` | struct | 8 | fields in order, each aligned |
| VARIANT | `v` | `Variant` | 1 | SIGNATURE + the value |
| DICT_ENTRY | `{KV}` | `map[K]V` | 8 | key then value; always inside an array |

Three mistakes that will silently corrupt messages — the tests in §8 exist to
catch exactly these:

1. **An array's length prefix counts bytes, not elements, and excludes the
   padding between the prefix and the first element.** For `a(oayays)` the
   prefix is followed by up to 4 padding bytes before the first struct.
2. **SIGNATURE uses a one-byte length**, unlike STRING's four.
3. **Struct alignment is 8 even for an empty struct**, and applies to each
   struct inside an array.

### Message layout

```
byte  0     endianness       'l'
byte  1     message type     1=METHOD_CALL 2=METHOD_RETURN 3=ERROR 4=SIGNAL
byte  2     flags            0, or 0x1 NO_REPLY_EXPECTED
byte  3     protocol version 1
uint32      body length in bytes
uint32      serial           starts at 1, monotonic, never 0
array of (byte code, variant value)   the header fields
<padding to 8>
body
```

Header field codes: `1` PATH (`o`), `2` INTERFACE (`s`), `3` MEMBER (`s`),
`4` ERROR_NAME (`s`), `5` REPLY_SERIAL (`u`), `6` DESTINATION (`s`),
`7` SENDER (`s`), `8` SIGNATURE (`g`).

A `METHOD_CALL` sets PATH, DESTINATION, INTERFACE, MEMBER, and SIGNATURE when
the body is non-empty. **The body starts at an 8-byte boundary after the header
array** — this padding is not counted in the body length.

### Authentication handshake

Before any message, over the connected socket:

```
→ \0                                 (a single NUL byte, not part of SASL)
→ AUTH EXTERNAL <uid-in-ascii-hex>\r\n
← OK <server-guid>\r\n
→ BEGIN\r\n
```

`<uid-in-ascii-hex>` is the decimal UID rendered as ASCII, then hex-encoded:
uid `1000` → the string `"1000"` → `31303030`. Do **not** hex-encode the
integer.

`NEGOTIATE_UNIX_FD` is not sent — this module does not pass file descriptors.
If the server answers `REJECTED`, return an error listing the mechanisms it
offered; do not attempt another mechanism.

### Address parsing

`$DBUS_SESSION_BUS_ADDRESS` holds one or more addresses separated by `;`. Try
each in order, and support exactly two forms:

| Form | Dial |
|---|---|
| `unix:path=/run/user/1000/bus` | `net.Dial("unix", "/run/user/1000/bus")` |
| `unix:abstract=/tmp/dbus-XYZ` | `net.Dial("unix", "\x00/tmp/dbus-XYZ")` — leading NUL |

Keys may appear in any order and may carry other keys (`guid=`) that are
ignored. Values are percent-escaped: `%2f` → `/`.

If the variable is unset, fall back to `unix:path=$XDG_RUNTIME_DIR/bus` when
`XDG_RUNTIME_DIR` is set, and otherwise return
`` `dbus: DBUS_SESSION_BUS_ADDRESS is not set` `` — verbatim.

### The read loop

One goroutine owns the socket reader. For each inbound message:

- `METHOD_RETURN` / `ERROR` → look up REPLY_SERIAL in a `map[uint32]chan *Reply`
  guarded by a mutex, deliver, delete the entry.
- `SIGNAL` → non-blocking send to every registered signal channel.
- anything else → discard.

A read error closes the connection and fails every pending call with that error,
so no caller blocks forever. `Call` must not wait without bound: use a
30-second timeout returning `` `dbus: call timed out` ``.

## 5. Files to create

| File | Contents |
|---|---|
| `dbus.go` | `Conn`, `SessionBus`, `Close`, `Object`, `Call`, `Reply`, `Store`, `GetProperty` |
| `address.go` | `$DBUS_SESSION_BUS_ADDRESS` parsing and dialling |
| `auth.go` | the NUL byte, `AUTH EXTERNAL`, `BEGIN` handshake |
| `message.go` | header encode/decode, serial allocation, message types |
| `encode.go` | marshalling for the §4 type set + signature derivation |
| `decode.go` | unmarshalling for the §4 type set |
| `variant.go` | `Variant`, `MakeVariant`, `ObjectPath` |
| `signal.go` | `Signal`, `Signals`, `AddMatch`, `RemoveMatch` |
| `errors.go` | typed errors (§6) |
| `tests/` | the test package (§8) |

No file carries a build tag.

## 6. Constants and errors — no string literals in logic

```go
const (
    busName      = "org.freedesktop.DBus"
    busPath      = ObjectPath("/org/freedesktop/DBus")
    busInterface = "org.freedesktop.DBus"

    methodHello      = busInterface + ".Hello"
    methodAddMatch   = busInterface + ".AddMatch"
    methodRemoveMatch = busInterface + ".RemoveMatch"

    propertiesInterface = "org.freedesktop.DBus.Properties"
    methodPropertiesGet = propertiesInterface + ".Get"

    envSessionBusAddress = "DBUS_SESSION_BUS_ADDRESS"
    envRuntimeDir        = "XDG_RUNTIME_DIR"

    callTimeout = 30 * time.Second
)
```

Errors are a comparable string type so `errors.Is` works without wrapping:

```go
type Error string
func (e Error) Error() string { return string(e) }

const (
    ErrNoSessionBus     Error = "dbus: DBUS_SESSION_BUS_ADDRESS is not set"
    ErrAuthFailed       Error = "dbus: EXTERNAL authentication rejected"
    ErrClosed           Error = "dbus: connection closed"
    ErrCallTimeout      Error = "dbus: call timed out"
    ErrUnsupportedType  Error = "dbus: unsupported type"
    ErrSignatureMismatch Error = "dbus: signature mismatch"
)
```

A remote error carries its D-Bus name, so callers can branch on it:

```go
type CallError struct {
    Name string // e.g. "org.freedesktop.DBus.Error.ServiceUnknown"
    Msg  string
}
func (e *CallError) Error() string { return e.Name + ": " + e.Msg }
```

## 7. Stages

| # | Stage | Deliverable | Acceptance |
|---|---|---|---|
| 1 | Types and codec | `variant.go`, `encode.go`, `decode.go` | round-trip tests in §8 pass with byte-exact golden vectors |
| 2 | Messages | `message.go` | a `METHOD_CALL` encodes to the golden byte sequence in §8.2 |
| 3 | Connection | `address.go`, `auth.go`, `dbus.go` | `SessionBus()` connects and `Hello` returns a unique name on a real desktop |
| 4 | Signals | `signal.go` | `AddMatch` + a received signal reaches a registered channel |
| 5 | Hardening | timeouts, close semantics | closing mid-call fails the pending call with `ErrClosed`, no goroutine leak |

## 8. Tests — `tests/` package, run with `gotest`

The codec is where bugs hide and where they are cheapest to catch, so it is
tested against **byte-exact golden vectors**, not round-trips alone. A
round-trip test passes happily with a consistently wrong encoder.

### 8.1 Golden encodings — assert these exact bytes

```
STRING "foo"                    → 03 00 00 00 66 6f 6f 00
SIGNATURE "s"                   → 01 73 00
OBJECT_PATH "/a"                → 02 00 00 00 2f 61 00
BOOLEAN true                    → 01 00 00 00
ARRAY []string{"a","b"}         → 0c 00 00 00  01 00 00 00 61 00  00 00  01 00 00 00 62 00
                                  ^len=12      ^"a" + NUL  ^pad     ^"b" + NUL
VARIANT MakeVariant("hi")       → 01 73 00 00  02 00 00 00 68 69 00
                                  ^sig "s"  ^pad to 4 for the string
```

The `ARRAY` vector is the important one: it proves the length prefix counts
bytes and that inter-element padding is applied but not counted.

### 8.2 Golden message

Encode a `METHOD_CALL` to `org.freedesktop.DBus` / `/org/freedesktop/DBus` /
`Hello`, serial 1, empty body. Assert the total length is a multiple of 8 and
that byte 0 is `'l'`, byte 1 is `1`, byte 3 is `1`, and the body-length field is
`0`.

### 8.3 Connection tests without a bus

`SessionBus()` must not be tested against the developer's real bus in CI.
Instead:

- Point `DBUS_SESSION_BUS_ADDRESS` at a `net.Listen("unix", ...)` socket in a
  `t.TempDir()`, run a fake server goroutine that performs the handshake and
  answers `Hello` with a unique name, and assert the client completes.
- The fake server also drives the error paths: `REJECTED` → `ErrAuthFailed`;
  closing the socket mid-call → `ErrClosed`.

This fake is the module's most valuable test asset. Put it in
`tests/fakebus_test.go` and keep it honest about the wire format — it decodes
real messages, it does not pattern-match bytes.

### 8.4 Address parsing table test

| Input | Expected network, address |
|---|---|
| `unix:path=/run/user/1000/bus` | `unix`, `/run/user/1000/bus` |
| `unix:abstract=/tmp/dbus-Ab` | `unix`, `"\x00/tmp/dbus-Ab"` |
| `unix:path=/x,guid=deadbeef` | `unix`, `/x` |
| `unix:path=%2frun%2fbus` | `unix`, `/run/bus` |
| `tcp:host=localhost,port=1` | error |
| `` (empty) | `ErrNoSessionBus` unless `XDG_RUNTIME_DIR` is set |

## 9. Documentation to write

- `README.md` — what this is, the explicit scope boundary from §1, and a
  ten-line example calling a method on the session bus.
- `docs/ARCHITECTURE.md` — the message layout diagram and the read-loop
  ownership model (who owns the socket, how replies are routed).
- `docs/WIRE_FORMAT.md` — §4 in full, as the reference the maintainer returns to.

## 10. Reference material

**Port, do not invent.** `github.com/godbus/dbus/v5` is a correct, widely
deployed implementation of this wire format, and it is **BSD-2-Clause** —
copying from it is permitted and encouraged. Its `auth.go`, `encoder.go`,
`decoder.go`, `message.go` and `transport_unix.go` already solve the alignment
and padding rules that are easy to get subtly wrong; take that logic.

What must **not** be carried over is its generality: object export, name
ownership, introspection, signal-matching DSL, and the reflection-driven codec
that accepts any Go type. Reduce the codec to the fixed type set in §4, and drop
everything outside the scope table in §1. That reduction is the point of the
module — the wire format is not where the savings are, the surface is.

Copyright obligation: any file with logic derived from `godbus` keeps a
BSD-2-Clause notice, and the repository gets a `NOTICE` naming it. The D-Bus
specification's "Message Protocol" chapter is the reference for anything
ambiguous.

The consumer that defines success is
`https://github.com/tinywasm/keyring/blob/main/docs/PLAN_STAGE_4_LINUX.md`,
which lists the seven Secret Service calls this module must support.
