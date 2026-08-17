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

## 4. The wire format — port it, do not derive it from the table below

**This entire module is a port of `github.com/godbus/dbus/v5@v5.1.0`
(BSD-2-Clause), narrowed to the scope in §1.** The appendices at the bottom of
this file (§11) are that library's actual source, for the exact version
`zalando/go-keyring` depended on. Every subsection below names which appendix
backs it. **Copy the logic from the named appendix and delete what §1 puts out
of scope — do not write a fresh implementation from the wire-format
description.** The tables here are a map of the appendix, not a specification
to implement independently; if a table and an appendix ever seem to disagree,
the appendix is the ground truth; fix the table instead of guessing.

D-Bus is a binary protocol. All integers are little-endian (the connection
always announces `'l'`). **Every type is aligned to its own boundary, and
padding bytes must be zero.** This is ported from **Appendix C (`sig.go`)**,
**Appendix D (`encoder.go`)** and **Appendix E (`decoder.go`)**.

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

### Authentication handshake — ported from Appendix A (`auth.go`, `auth_external.go`)

Before any message, over the connected socket:

```
→ \0                                 (a single NUL byte, not part of SASL)
→ AUTH EXTERNAL <uid-in-ascii-hex>\r\n
← OK <server-guid>\r\n
→ BEGIN\r\n
```

`<uid-in-ascii-hex>` is the decimal UID rendered as ASCII, then hex-encoded:
uid `1000` → the string `"1000"` → `31303030`. Do **not** hex-encode the
integer. This is exactly `auth_external.go`'s `authExternal.FirstData` — port
it unchanged, it is 20 lines.

Appendix A's `Conn.Auth` and `tryAuth` implement the full SASL state machine
(`waitingForData` / `waitingForOk` / `waitingForReject`) for a *list* of
mechanisms, including `NEGOTIATE_UNIX_FD` and `DBUS_COOKIE_SHA1` fallback.
**Port the state machine, narrowed to one mechanism:** drop the `methods
[]Auth` loop (there is only `EXTERNAL`), drop the `NEGOTIATE_UNIX_FD` branch
entirely (§1 excludes FD passing), and drop `DBUS_COOKIE_SHA1`
(`auth_sha1.go` — not appended, out of scope). What remains is
`authReadLine`/`authWriteLine` plus the `tryAuth` state transitions for
`DATA`/`OK`/`REJECTED`/`ERROR`, which is the part that is easy to get subtly
wrong (e.g. `CANCEL` on a malformed `OK` line) and expensive to re-derive from
the SASL RFC instead of copying.

If the server answers `REJECTED` (no matching mechanism), return an error
listing the mechanisms it offered; do not attempt another mechanism, since
`EXTERNAL` is the only one this module speaks.

### Address parsing — ported from Appendix G (`conn.go` excerpt) and Appendix H (`transport_unix.go`)

`$DBUS_SESSION_BUS_ADDRESS` holds one or more addresses separated by `;`. Try
each in order, and support exactly two forms — this is `getKey` (Appendix G)
plus `newUnixTransport` (Appendix H), narrowed:

| Form | Dial |
|---|---|
| `unix:path=/run/user/1000/bus` | `net.DialUnix("unix", nil, &net.UnixAddr{Name: path})` |
| `unix:abstract=/tmp/dbus-XYZ` | same, with `Name: "@" + abstract` — godbus's `@` prefix is equivalent to this plan's leading-NUL description; port the `@` form, it is what the appendix actually does |

Keys may appear in any order and may carry other keys (`guid=`) that are
ignored — `getKey` already handles this by scanning comma-separated `key=value`
pairs and returning the first match. Values are percent-escaped: `%2f` → `/`;
port `getTransport`'s address-splitting, but **drop everything about TCP and
nonce-TCP transports** (`transport_tcp.go`, `transport_nonce_tcp.go` — not
appended, out of scope per §1).

**Do not port `getSessionBusAddress`'s autolaunch/`tryDiscoverDbusSessionBusAddress`
fallback** — §1 excludes autolaunch. If `$DBUS_SESSION_BUS_ADDRESS` is unset,
fall back to `unix:path=$XDG_RUNTIME_DIR/bus` when `XDG_RUNTIME_DIR` is set, and
otherwise return `` `dbus: DBUS_SESSION_BUS_ADDRESS is not set` `` — verbatim.
This fallback is this module's own addition, not in the appendix, because
`godbus` assumes autolaunch is always available as its last resort and this
module deliberately does not implement autolaunch.

### The read loop — ported from Appendix G (`conn.go` excerpt: `inWorker`, `callTracker`)

`inWorker` (Appendix G) is the reference: one goroutine owns the socket reader,
decodes each message, and dispatches by `msg.Type`. Port its **shape**, narrowed:

- `METHOD_RETURN` / `ERROR` → what godbus's `callTracker.handleReply` /
  `handleDBusError` do: look up the reply serial in a map guarded by a mutex,
  deliver to the waiting caller, delete the entry. This module's map is
  `map[uint32]chan *Reply` rather than godbus's `*Call` structure — simpler,
  because §3 has no `Go`-style async call, only the blocking `Call`.
- `SIGNAL` → what `handleSignal` does, minus the `NameLost`/`NameAcquired`
  bookkeeping (that belongs to godbus's name-ownership tracking, which this
  module does not have): decode `Sender`/`Path`/interface+member into this
  module's `Signal`, and non-blocking-send it to every registered channel.
- `TypeMethodCall` → **do not port this branch.** `inWorker` spawns
  `conn.handleCall(msg)` because godbus can export objects and answer incoming
  calls; this module cannot (§1 scope table: "Exporting objects" is out of
  scope). Discard method-call messages instead.
- read error → what `inWorker`'s error branch does: close the connection and
  fail every pending call with that error, so no caller blocks forever. Port
  `callTracker.finalizeAllWithError`'s intent even though this module's call
  tracking is a plain map, not godbus's `callTracker` type.

`Call` must not wait without bound: use a 30-second timeout returning
`` `dbus: call timed out` `` — this timeout is this module's own addition;
godbus's blocking `<-call.Done` has no timeout because its callers manage that
themselves via context, which this module's narrower API does not expose.

## 5. Files to create

| File | Contents | Ported from |
|---|---|---|
| `dbus.go` | `Conn`, `SessionBus`, `Close`, `Object`, `Call`, `Reply`, `Store`, `GetProperty` | Appendix G excerpt (`Hello`, `send`/`Call` chain) |
| `address.go` | `$DBUS_SESSION_BUS_ADDRESS` parsing and dialling | Appendix G excerpt (`getKey`, `getTransport`) + Appendix H |
| `auth.go` | the NUL byte, `AUTH EXTERNAL`, `BEGIN` handshake | Appendix A |
| `message.go` | header encode/decode, serial allocation, message types | Appendix F |
| `encode.go` | marshalling for the §4 type set + signature derivation | Appendix D (+ Appendix C for signatures) |
| `decode.go` | unmarshalling for the §4 type set | Appendix E |
| `variant.go` | `Variant`, `MakeVariant`, `ObjectPath` | Appendix B |
| `signal.go` | `Signal`, `Signals`, `AddMatch`, `RemoveMatch` | Appendix G excerpt (`handleSignal`, narrowed) |
| `errors.go` | typed errors (§6) | this module's own — no upstream equivalent |
| `tests/` | the test package (§8) | Appendices K–O |

No file carries a build tag. Every ported file keeps a BSD-2-Clause notice per
§10; `dbus.go`'s Store is written fresh (not ported) since it is intentionally
non-reflective — see the design note in §3.

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

## 8. Tests — port them into `tests/`, do not write fresh ones from scratch

**Copy first, adapt second.** Appendices K through O (§11) are
`godbus/dbus/v5`'s actual test files for exactly the layer this module ports:
the codec and the message envelope. They already are byte-exact golden-vector
tests — precisely the discipline this section used to ask the executor to
invent from a handful of examples. Port them; only then check whether the
handful of cases below are already covered (they are, almost entirely) and add
what genuinely is not.

| Appendix | Upstream file | Becomes | Prune |
|---|---|---|---|
| K | `sig_test.go` | `tests/sig_test.go` | nothing — fully in scope |
| L | `variant_test.go` | `tests/variant_test.go` | any case touching types outside §4's set |
| M | `encoder_test.go` | `tests/encoder_test.go` | struct/slice-of-struct cases using Go struct tags or reflection-driven signature inference — this module's encoder takes the §4 concrete types directly, no struct tags |
| N | `decoder_test.go` | `tests/decoder_test.go` | same pruning as M |
| O | `message_test.go` | `tests/message_test.go` | `UnixFDs`/`UnixFDIndex` cases — §1 excludes FD passing |

Adapt each ported file mechanically: package name (`dbus_test`, calling the
public API — these are internal `package dbus` tests upstream, and this module
keeps its codec unexported, so either move the test package in-tree as
`package dbus` under `tests/` if Go tooling in this repo expects that layout,
or expose the minimal internal hooks the tests need; do not weaken a test to
avoid this, ask instead if the two don't reconcile cleanly). Import paths have
no upstream module to strip since this module has no dependencies.

**What is not ported, and is written fresh instead** — because upstream tests
these against a real bus and this module deliberately never does:

### 8.1 Connection tests without a bus

`SessionBus()` must not be tested against the developer's real bus in CI, and
`conn_test.go`/`transport_unix_test.go` (not appended — they require a live
`dbus-daemon` and exercise unix-FD passing and object export, both out of
scope) are not the model to follow here. Instead:

- Point `DBUS_SESSION_BUS_ADDRESS` at a `net.Listen("unix", ...)` socket in a
  `t.TempDir()`, run a fake server goroutine that performs the handshake
  (ported logic from Appendix A, server side) and answers `Hello` with a
  unique name, and assert the client completes.
- The fake server also drives the error paths: `REJECTED` → `ErrAuthFailed`;
  closing the socket mid-call → `ErrClosed`.

This fake is the module's most valuable test asset precisely because upstream
has no equivalent (its tests assume a real daemon). Put it in
`tests/fakebus_test.go` and keep it honest about the wire format — it decodes
real messages using this module's own decoder, it does not pattern-match bytes.

### 8.2 Address parsing table test

Not upstream either — `getSessionBusAddress`'s autolaunch fallback (which this
module deliberately omits, §"Address parsing") has no test to port from. Write:

| Input | Expected network, address |
|---|---|
| `unix:path=/run/user/1000/bus` | `unix`, `/run/user/1000/bus` |
| `unix:abstract=/tmp/dbus-Ab` | `unix`, `"@/tmp/dbus-Ab"` |
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

## 10. Licensing and what the appendices are

**Port, do not invent — this is not optional guidance, it is the method for
every section above.** The appendices in §11 are the actual source of
`github.com/godbus/dbus/v5@v5.1.0`, the exact version `zalando/go-keyring`
depended on, reproduced here so the executor never needs network access or a
`go mod download` to get them. It is **BSD-2-Clause** — copying is permitted
and expected.

What must **not** be carried over is godbus's generality: object export
(`export.go`, `server_interfaces.go` — not appended), name ownership,
introspection, the reflection-driven `Store`/type-inference codec in
`dbus.go` (not appended — see §3's design note on why this module's `Store` is
written fresh instead), unix-FD passing, TCP/nonce-TCP transports, and
`DBUS_COOKIE_SHA1` auth (`auth_sha1.go` — not appended). Each subsection in §4
already says exactly what to drop from its appendix; that reduction — the
surface, not the wire format — is the entire point of this module existing
instead of a `go.mod` line pointing at godbus.

**Copyright obligation:** every file in this module with logic derived from an
appendix keeps a `// Portions Copyright (c) 2013, Georg Reinke, Google —
BSD-2-Clause` notice at its top, and the repository root gets a `NOTICE` file
naming `godbus/dbus/v5` and the files it covers. The D-Bus specification's
"Message Protocol" chapter is the reference for anything the appendices leave
ambiguous — but reach for an appendix before reaching for the spec.

The consumer that defines success is
`https://github.com/tinywasm/keyring/blob/main/docs/PLAN_STAGE_4_LINUX.md`,
which lists the seven Secret Service calls this module must support.

## 11. Appendices — source to port

All of the following is `github.com/godbus/dbus/v5@v5.1.0`, copyright (c) 2013
Georg Reinke (guelfey at gmail dot com), Google, **BSD-2-Clause**. Reproduced
verbatim (Appendices G is an excerpt of `conn.go`, noted where cut).

### Appendix A — `auth.go`

The SASL state machine. Port narrowed to EXTERNAL only — see §"Authentication handshake".

```go
package dbus

import (
	"bufio"
	"bytes"
	"errors"
	"io"
	"os"
	"strconv"
)

// AuthStatus represents the Status of an authentication mechanism.
type AuthStatus byte

const (
	// AuthOk signals that authentication is finished; the next command
	// from the server should be an OK.
	AuthOk AuthStatus = iota

	// AuthContinue signals that additional data is needed; the next command
	// from the server should be a DATA.
	AuthContinue

	// AuthError signals an error; the server sent invalid data or some
	// other unexpected thing happened and the current authentication
	// process should be aborted.
	AuthError
)

type authState byte

const (
	waitingForData authState = iota
	waitingForOk
	waitingForReject
)

// Auth defines the behaviour of an authentication mechanism.
type Auth interface {
	// Return the name of the mechanism, the argument to the first AUTH command
	// and the next status.
	FirstData() (name, resp []byte, status AuthStatus)

	// Process the given DATA command, and return the argument to the DATA
	// command and the next status. If len(resp) == 0, no DATA command is sent.
	HandleData(data []byte) (resp []byte, status AuthStatus)
}

// Auth authenticates the connection, trying the given list of authentication
// mechanisms (in that order). If nil is passed, the EXTERNAL and
// DBUS_COOKIE_SHA1 mechanisms are tried for the current user. For private
// connections, this method must be called before sending any messages to the
// bus. Auth must not be called on shared connections.
func (conn *Conn) Auth(methods []Auth) error {
	if methods == nil {
		uid := strconv.Itoa(os.Geteuid())
		methods = []Auth{AuthExternal(uid), AuthCookieSha1(uid, getHomeDir())}
	}
	in := bufio.NewReader(conn.transport)
	err := conn.transport.SendNullByte()
	if err != nil {
		return err
	}
	err = authWriteLine(conn.transport, []byte("AUTH"))
	if err != nil {
		return err
	}
	s, err := authReadLine(in)
	if err != nil {
		return err
	}
	if len(s) < 2 || !bytes.Equal(s[0], []byte("REJECTED")) {
		return errors.New("dbus: authentication protocol error")
	}
	s = s[1:]
	for _, v := range s {
		for _, m := range methods {
			if name, _, status := m.FirstData(); bytes.Equal(v, name) {
				var ok bool
				err = authWriteLine(conn.transport, []byte("AUTH"), v)
				if err != nil {
					return err
				}
				switch status {
				case AuthOk:
					err, ok = conn.tryAuth(m, waitingForOk, in)
				case AuthContinue:
					err, ok = conn.tryAuth(m, waitingForData, in)
				default:
					panic("dbus: invalid authentication status")
				}
				if err != nil {
					return err
				}
				if ok {
					if conn.transport.SupportsUnixFDs() {
						err = authWriteLine(conn, []byte("NEGOTIATE_UNIX_FD"))
						if err != nil {
							return err
						}
						line, err := authReadLine(in)
						if err != nil {
							return err
						}
						switch {
						case bytes.Equal(line[0], []byte("AGREE_UNIX_FD")):
							conn.EnableUnixFDs()
							conn.unixFD = true
						case bytes.Equal(line[0], []byte("ERROR")):
						default:
							return errors.New("dbus: authentication protocol error")
						}
					}
					err = authWriteLine(conn.transport, []byte("BEGIN"))
					if err != nil {
						return err
					}
					go conn.inWorker()
					return nil
				}
			}
		}
	}
	return errors.New("dbus: authentication failed")
}

// tryAuth tries to authenticate with m as the mechanism, using state as the
// initial authState and in for reading input. It returns (nil, true) on
// success, (nil, false) on a REJECTED and (someErr, false) if some other
// error occurred.
func (conn *Conn) tryAuth(m Auth, state authState, in *bufio.Reader) (error, bool) {
	for {
		s, err := authReadLine(in)
		if err != nil {
			return err, false
		}
		switch {
		case state == waitingForData && string(s[0]) == "DATA":
			if len(s) != 2 {
				err = authWriteLine(conn.transport, []byte("ERROR"))
				if err != nil {
					return err, false
				}
				continue
			}
			data, status := m.HandleData(s[1])
			switch status {
			case AuthOk, AuthContinue:
				if len(data) != 0 {
					err = authWriteLine(conn.transport, []byte("DATA"), data)
					if err != nil {
						return err, false
					}
				}
				if status == AuthOk {
					state = waitingForOk
				}
			case AuthError:
				err = authWriteLine(conn.transport, []byte("ERROR"))
				if err != nil {
					return err, false
				}
			}
		case state == waitingForData && string(s[0]) == "REJECTED":
			return nil, false
		case state == waitingForData && string(s[0]) == "ERROR":
			err = authWriteLine(conn.transport, []byte("CANCEL"))
			if err != nil {
				return err, false
			}
			state = waitingForReject
		case state == waitingForData && string(s[0]) == "OK":
			if len(s) != 2 {
				err = authWriteLine(conn.transport, []byte("CANCEL"))
				if err != nil {
					return err, false
				}
				state = waitingForReject
			} else {
				conn.uuid = string(s[1])
				return nil, true
			}
		case state == waitingForData:
			err = authWriteLine(conn.transport, []byte("ERROR"))
			if err != nil {
				return err, false
			}
		case state == waitingForOk && string(s[0]) == "OK":
			if len(s) != 2 {
				err = authWriteLine(conn.transport, []byte("CANCEL"))
				if err != nil {
					return err, false
				}
				state = waitingForReject
			} else {
				conn.uuid = string(s[1])
				return nil, true
			}
		case state == waitingForOk && string(s[0]) == "DATA":
			err = authWriteLine(conn.transport, []byte("DATA"))
			if err != nil {
				return err, false
			}
		case state == waitingForOk && string(s[0]) == "REJECTED":
			return nil, false
		case state == waitingForOk && string(s[0]) == "ERROR":
			err = authWriteLine(conn.transport, []byte("CANCEL"))
			if err != nil {
				return err, false
			}
			state = waitingForReject
		case state == waitingForOk:
			err = authWriteLine(conn.transport, []byte("ERROR"))
			if err != nil {
				return err, false
			}
		case state == waitingForReject && string(s[0]) == "REJECTED":
			return nil, false
		case state == waitingForReject:
			return errors.New("dbus: authentication protocol error"), false
		default:
			panic("dbus: invalid auth state")
		}
	}
}

// authReadLine reads a line and separates it into its fields.
func authReadLine(in *bufio.Reader) ([][]byte, error) {
	data, err := in.ReadBytes('\n')
	if err != nil {
		return nil, err
	}
	data = bytes.TrimSuffix(data, []byte("\r\n"))
	return bytes.Split(data, []byte{' '}), nil
}

// authWriteLine writes the given line in the authentication protocol format
// (elements of data separated by a " " and terminated by "\r\n").
func authWriteLine(out io.Writer, data ...[]byte) error {
	buf := make([]byte, 0)
	for i, v := range data {
		buf = append(buf, v...)
		if i != len(data)-1 {
			buf = append(buf, ' ')
		}
	}
	buf = append(buf, '\r')
	buf = append(buf, '\n')
	n, err := out.Write(buf)
	if err != nil {
		return err
	}
	if n != len(buf) {
		return io.ErrUnexpectedEOF
	}
	return nil
}
```

### Appendix A — `auth_external.go`

The EXTERNAL mechanism itself — port unchanged, it is already minimal.

```go
package dbus

import (
	"encoding/hex"
)

// AuthExternal returns an Auth that authenticates as the given user with the
// EXTERNAL mechanism.
func AuthExternal(user string) Auth {
	return authExternal{user}
}

// AuthExternal implements the EXTERNAL authentication mechanism.
type authExternal struct {
	user string
}

func (a authExternal) FirstData() ([]byte, []byte, AuthStatus) {
	b := make([]byte, 2*len(a.user))
	hex.Encode(b, []byte(a.user))
	return []byte("EXTERNAL"), b, AuthOk
}

func (a authExternal) HandleData(b []byte) ([]byte, AuthStatus) {
	return nil, AuthError
}
```

### Appendix B — `variant.go`

```go
package dbus

import (
	"bytes"
	"fmt"
	"reflect"
	"sort"
	"strconv"
)

// Variant represents the D-Bus variant type.
type Variant struct {
	sig   Signature
	value interface{}
}

// MakeVariant converts the given value to a Variant. It panics if v cannot be
// represented as a D-Bus type.
func MakeVariant(v interface{}) Variant {
	return MakeVariantWithSignature(v, SignatureOf(v))
}

// MakeVariantWithSignature converts the given value to a Variant.
func MakeVariantWithSignature(v interface{}, s Signature) Variant {
	return Variant{s, v}
}

// ParseVariant parses the given string as a variant as described at
// https://developer.gnome.org/glib/stable/gvariant-text.html. If sig is not
// empty, it is taken to be the expected signature for the variant.
func ParseVariant(s string, sig Signature) (Variant, error) {
	tokens := varLex(s)
	p := &varParser{tokens: tokens}
	n, err := varMakeNode(p)
	if err != nil {
		return Variant{}, err
	}
	if sig.str == "" {
		sig, err = varInfer(n)
		if err != nil {
			return Variant{}, err
		}
	}
	v, err := n.Value(sig)
	if err != nil {
		return Variant{}, err
	}
	return MakeVariant(v), nil
}

// format returns a formatted version of v and whether this string can be parsed
// unambiguously.
func (v Variant) format() (string, bool) {
	switch v.sig.str[0] {
	case 'b', 'i':
		return fmt.Sprint(v.value), true
	case 'n', 'q', 'u', 'x', 't', 'd', 'h':
		return fmt.Sprint(v.value), false
	case 's':
		return strconv.Quote(v.value.(string)), true
	case 'o':
		return strconv.Quote(string(v.value.(ObjectPath))), false
	case 'g':
		return strconv.Quote(v.value.(Signature).str), false
	case 'v':
		s, unamb := v.value.(Variant).format()
		if !unamb {
			return "<@" + v.value.(Variant).sig.str + " " + s + ">", true
		}
		return "<" + s + ">", true
	case 'y':
		return fmt.Sprintf("%#x", v.value.(byte)), false
	}
	rv := reflect.ValueOf(v.value)
	switch rv.Kind() {
	case reflect.Slice:
		if rv.Len() == 0 {
			return "[]", false
		}
		unamb := true
		buf := bytes.NewBuffer([]byte("["))
		for i := 0; i < rv.Len(); i++ {
			// TODO: slooow
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

// Signature returns the D-Bus signature of the underlying value of v.
func (v Variant) Signature() Signature {
	return v.sig
}

// String returns the string representation of the underlying value of v as
// described at https://developer.gnome.org/glib/stable/gvariant-text.html.
func (v Variant) String() string {
	s, unamb := v.format()
	if !unamb {
		return "@" + v.sig.str + " " + s
	}
	return s
}

// Value returns the underlying value of v.
func (v Variant) Value() interface{} {
	return v.value
}

// Store converts the variant into a native go type using the same
// mechanism as the "Store" function.
func (v Variant) Store(value interface{}) error {
	return storeInterfaces(v.value, value)
}
```

### Appendix C — `sig.go`

Signature derivation and alignment/size tables — the source of truth for §4's alignment column.

```go
package dbus

import (
	"fmt"
	"reflect"
	"strings"
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
	'h': unixFDIndexType,
}

// Signature represents a correct type signature as specified by the D-Bus
// specification. The zero value represents the empty signature, "".
type Signature struct {
	str string
}

// SignatureOf returns the concatenation of all the signatures of the given
// values. It panics if one of them is not representable in D-Bus.
func SignatureOf(vs ...interface{}) Signature {
	var s string
	for _, v := range vs {
		s += getSignature(reflect.TypeOf(v), &depthCounter{})
	}
	return Signature{s}
}

// SignatureOfType returns the signature of the given type. It panics if the
// type is not representable in D-Bus.
func SignatureOfType(t reflect.Type) Signature {
	return Signature{getSignature(t, &depthCounter{})}
}

// getSignature returns the signature of the given type and panics on unknown types.
func getSignature(t reflect.Type, depth *depthCounter) (sig string) {
	if !depth.Valid() {
		panic("container nesting too deep")
	}
	defer func() {
		if len(sig) > 255 {
			panic("signature exceeds the length limitation")
		}
	}()
	// handle simple types first
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
		if t == unixFDType {
			return "h"
		}
		return "i"
	case reflect.Uint, reflect.Uint32:
		if t == unixFDIndexType {
			return "h"
		}
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
			panic(InvalidTypeError{t})
		}
		return "(" + s + ")"
	case reflect.Array, reflect.Slice:
		return "a" + getSignature(t.Elem(), depth.EnterArray())
	case reflect.Map:
		if !isKeyType(t.Key()) {
			panic(InvalidTypeError{t})
		}
		return "a{" + getSignature(t.Key(), depth.EnterArray().EnterDictEntry()) + getSignature(t.Elem(), depth.EnterArray().EnterDictEntry()) + "}"
	case reflect.Interface:
		return "v"
	}
	panic(InvalidTypeError{t})
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
	return err != nil && r == ""
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

// Try to read a single type from this string. If it was successful, err is nil
// and rem is the remaining unparsed part. Otherwise, err is a non-nil
// SignatureError and rem is "". depth is the current recursion depth which may
// not be greater than 64 and should be given as 0 on the first call.
func validSingle(s string, depth *depthCounter) (err error, rem string) {
	if s == "" {
		return SignatureError{Sig: s, Reason: "empty signature"}, ""
	}
	if !depth.Valid() {
		return SignatureError{Sig: s, Reason: "container nesting too deep"}, ""
	}
	switch s[0] {
	case 'y', 'b', 'n', 'q', 'i', 'u', 'x', 't', 'd', 's', 'g', 'o', 'v', 'h':
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

// typeFor returns the type of the given signature. It ignores any left over
// characters and panics if s doesn't start with a valid type signature.
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
```

### Appendix D — `encoder.go`

```go
package dbus

import (
	"bytes"
	"encoding/binary"
	"io"
	"reflect"
	"strings"
	"unicode/utf8"
)

// An encoder encodes values to the D-Bus wire format.
type encoder struct {
	out   io.Writer
	fds   []int
	order binary.ByteOrder
	pos   int
}

// NewEncoder returns a new encoder that writes to out in the given byte order.
func newEncoder(out io.Writer, order binary.ByteOrder, fds []int) *encoder {
	enc := newEncoderAtOffset(out, 0, order, fds)
	return enc
}

// newEncoderAtOffset returns a new encoder that writes to out in the given
// byte order. Specify the offset to initialize pos for proper alignment
// computation.
func newEncoderAtOffset(out io.Writer, offset int, order binary.ByteOrder, fds []int) *encoder {
	enc := new(encoder)
	enc.out = out
	enc.order = order
	enc.pos = offset
	enc.fds = fds
	return enc
}

// Aligns the next output to be on a multiple of n. Panics on write errors.
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

// pad returns the number of bytes of padding, based on current position and additional offset.
// and alignment.
func (enc *encoder) padding(offset, algn int) int {
	abs := enc.pos + offset
	if abs%algn != 0 {
		newabs := (abs + algn - 1) & ^(algn - 1)
		return newabs - abs
	}
	return 0
}

// Calls binary.Write(enc.out, enc.order, v) and panics on write errors.
func (enc *encoder) binwrite(v interface{}) {
	if err := binary.Write(enc.out, enc.order, v); err != nil {
		panic(err)
	}
}

// Encode encodes the given values to the underlying reader. All written values
// are aligned properly as required by the D-Bus spec.
func (enc *encoder) Encode(vs ...interface{}) (err error) {
	defer func() {
		err, _ = recover().(error)
	}()
	for _, v := range vs {
		enc.encode(reflect.ValueOf(v), 0)
	}
	return nil
}

// encode encodes the given value to the writer and panics on error. depth holds
// the depth of the container nesting.
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
		if v.Type() == unixFDType {
			fd := v.Int()
			idx := len(enc.fds)
			enc.fds = append(enc.fds, int(fd))
			enc.binwrite(uint32(idx))
		} else {
			enc.binwrite(int32(v.Int()))
		}
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
		enc.encode(v.Elem(), depth)
	case reflect.Slice, reflect.Array:
		// Lookahead offset: 4 bytes for uint32 length (with alignment),
		// plus alignment for elements.
		n := enc.padding(0, 4) + 4
		offset := enc.pos + n + enc.padding(n, alignment(v.Type().Elem()))

		var buf bytes.Buffer
		bufenc := newEncoderAtOffset(&buf, offset, enc.order, enc.fds)

		for i := 0; i < v.Len(); i++ {
			bufenc.encode(v.Index(i), depth+1)
		}

		if buf.Len() > 1<<26 {
			panic(FormatError("input exceeds array size limitation"))
		}

		enc.fds = bufenc.fds
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
		// Maps are arrays of structures, so they actually increase the depth by
		// 2.
		if !isKeyType(v.Type().Key()) {
			panic(InvalidTypeError{v.Type()})
		}
		keys := v.MapKeys()
		// Lookahead offset: 4 bytes for uint32 length (with alignment),
		// plus 8-byte alignment
		n := enc.padding(0, 4) + 4
		offset := enc.pos + n + enc.padding(n, 8)

		var buf bytes.Buffer
		bufenc := newEncoderAtOffset(&buf, offset, enc.order, enc.fds)
		for _, k := range keys {
			bufenc.align(8)
			bufenc.encode(k, depth+2)
			bufenc.encode(v.MapIndex(k), depth+2)
		}
		enc.fds = bufenc.fds
		enc.encode(reflect.ValueOf(uint32(buf.Len())), depth)
		length := buf.Len()
		enc.align(8)
		if _, err := buf.WriteTo(enc.out); err != nil {
			panic(err)
		}
		enc.pos += length
	case reflect.Interface:
		enc.encode(reflect.ValueOf(MakeVariant(v.Interface())), depth)
	default:
		panic(InvalidTypeError{v.Type()})
	}
}
```

### Appendix E — `decoder.go`

```go
package dbus

import (
	"encoding/binary"
	"io"
	"reflect"
)

type decoder struct {
	in    io.Reader
	order binary.ByteOrder
	pos   int
	fds   []int
}

// newDecoder returns a new decoder that reads values from in. The input is
// expected to be in the given byte order.
func newDecoder(in io.Reader, order binary.ByteOrder, fds []int) *decoder {
	dec := new(decoder)
	dec.in = in
	dec.order = order
	dec.fds = fds
	return dec
}

// align aligns the input to the given boundary and panics on error.
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

// Calls binary.Read(dec.in, dec.order, v) and panics on read errors.
func (dec *decoder) binread(v interface{}) {
	if err := binary.Read(dec.in, dec.order, v); err != nil {
		panic(err)
	}
}

func (dec *decoder) Decode(sig Signature) (vs []interface{}, err error) {
	defer func() {
		var ok bool
		v := recover()
		if err, ok = v.(error); ok {
			if err == io.EOF || err == io.ErrUnexpectedEOF {
				err = FormatError("unexpected EOF")
			}
		}
	}()
	vs = make([]interface{}, 0)
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

func (dec *decoder) decode(s string, depth int) interface{} {
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
	case 'h':
		idx := dec.decode("u", depth).(uint32)
		if int(idx) < len(dec.fds) {
			return UnixFD(dec.fds[idx])
		}
		return UnixFDIndex(idx)
	case 'a':
		if len(s) > 1 && s[1] == '{' {
			ksig := s[2:3]
			vsig := s[3 : len(s)-1]
			v := reflect.MakeMap(reflect.MapOf(typeFor(ksig), typeFor(vsig)))
			if depth >= 63 {
				panic(FormatError("input exceeds container depth limit"))
			}
			length := dec.decode("u", depth).(uint32)
			// Even for empty maps, the correct padding must be included
			dec.align(8)
			spos := dec.pos
			for dec.pos < spos+int(length) {
				dec.align(8)
				if !isKeyType(v.Type().Key()) {
					panic(InvalidTypeError{v.Type()})
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
		// capacity can be determined only for fixed-size element types
		var capacity int
		if s := sigByteSize(sig); s != 0 {
			capacity = int(length) / s
		}
		v := reflect.MakeSlice(reflect.SliceOf(typeFor(sig)), 0, capacity)
		// Even for empty arrays, the correct padding must be included
		align := alignment(typeFor(s[1:]))
		if len(s) > 1 && s[1] == '(' {
			//Special case for arrays of structs
			//structs decode as a slice of interface{} values
			//but the dbus alignment does not match this
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
		v := make([]interface{}, 0)
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

// sigByteSize tries to calculates size of the given signature in bytes.
//
// It returns zero when it can't, for example when it contains non-fixed size
// types such as strings, maps and arrays that require reading of the transmitted
// data, for that we would need to implement the unread method for Decoder first.
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
		case 'b', 'i', 'u', 'h':
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

// A FormatError is an error in the wire format.
type FormatError string

func (e FormatError) Error() string {
	return "dbus: wire format error: " + string(e)
}
```

### Appendix F — `message.go`

```go
package dbus

import (
	"bytes"
	"encoding/binary"
	"errors"
	"io"
	"reflect"
	"strconv"
)

const protoVersion byte = 1

// Flags represents the possible flags of a D-Bus message.
type Flags byte

const (
	// FlagNoReplyExpected signals that the message is not expected to generate
	// a reply. If this flag is set on outgoing messages, any possible reply
	// will be discarded.
	FlagNoReplyExpected Flags = 1 << iota
	// FlagNoAutoStart signals that the message bus should not automatically
	// start an application when handling this message.
	FlagNoAutoStart
	// FlagAllowInteractiveAuthorization may be set on a method call
	// message to inform the receiving side that the caller is prepared
	// to wait for interactive authorization, which might take a
	// considerable time to complete. For instance, if this flag is set,
	// it would be appropriate to query the user for passwords or
	// confirmation via Polkit or a similar framework.
	FlagAllowInteractiveAuthorization
)

// Type represents the possible types of a D-Bus message.
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

// HeaderField represents the possible byte codes for the headers
// of a D-Bus message.
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

// An InvalidMessageError describes the reason why a D-Bus message is regarded as
// invalid.
type InvalidMessageError string

func (e InvalidMessageError) Error() string {
	return "dbus: invalid message: " + string(e)
}

// fieldType are the types of the various header fields.
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

// requiredFields lists the header fields that are required by the different
// message types.
var requiredFields = [typeMax][]HeaderField{
	TypeMethodCall:  {FieldPath, FieldMember},
	TypeMethodReply: {FieldReplySerial},
	TypeError:       {FieldErrorName, FieldReplySerial},
	TypeSignal:      {FieldPath, FieldInterface, FieldMember},
}

// Message represents a single D-Bus message.
type Message struct {
	Type
	Flags
	Headers map[HeaderField]Variant
	Body    []interface{}

	serial uint32
}

type header struct {
	Field byte
	Variant
}

func DecodeMessageWithFDs(rd io.Reader, fds []int) (msg *Message, err error) {
	var order binary.ByteOrder
	var hlength, length uint32
	var typ, flags, proto byte
	var headers []header

	b := make([]byte, 1)
	_, err = rd.Read(b)
	if err != nil {
		return
	}
	switch b[0] {
	case 'l':
		order = binary.LittleEndian
	case 'B':
		order = binary.BigEndian
	default:
		return nil, InvalidMessageError("invalid byte order")
	}

	dec := newDecoder(rd, order, fds)
	dec.pos = 1

	msg = new(Message)
	vs, err := dec.Decode(Signature{"yyyuu"})
	if err != nil {
		return nil, err
	}
	if err = Store(vs, &typ, &flags, &proto, &length, &msg.serial); err != nil {
		return nil, err
	}
	msg.Type = Type(typ)
	msg.Flags = Flags(flags)

	// get the header length separately because we need it later
	b = make([]byte, 4)
	_, err = io.ReadFull(rd, b)
	if err != nil {
		return nil, err
	}
	binary.Read(bytes.NewBuffer(b), order, &hlength)
	if hlength+length+16 > 1<<27 {
		return nil, InvalidMessageError("message is too long")
	}
	dec = newDecoder(io.MultiReader(bytes.NewBuffer(b), rd), order, fds)
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
	sig, _ := msg.Headers[FieldSignature].value.(Signature)
	if sig.str != "" {
		buf := bytes.NewBuffer(body)
		dec = newDecoder(buf, order, fds)
		vs, err := dec.Decode(sig)
		if err != nil {
			return nil, err
		}
		msg.Body = vs
	}

	return
}

// DecodeMessage tries to decode a single message in the D-Bus wire format
// from the given reader. The byte order is figured out from the first byte.
// The possibly returned error can be an error of the underlying reader, an
// InvalidMessageError or a FormatError.
func DecodeMessage(rd io.Reader) (msg *Message, err error) {
	return DecodeMessageWithFDs(rd, make([]int, 0))
}

type nullwriter struct{}

func (nullwriter) Write(p []byte) (cnt int, err error) {
	return len(p), nil
}

func (msg *Message) CountFds() (int, error) {
	if len(msg.Body) == 0 {
		return 0, nil
	}
	enc := newEncoder(nullwriter{}, nativeEndian, make([]int, 0))
	err := enc.Encode(msg.Body...)
	return len(enc.fds), err
}

func (msg *Message) EncodeToWithFDs(out io.Writer, order binary.ByteOrder) (fds []int, err error) {
	if err := msg.validateHeader(); err != nil {
		return nil, err
	}
	var vs [7]interface{}
	switch order {
	case binary.LittleEndian:
		vs[0] = byte('l')
	case binary.BigEndian:
		vs[0] = byte('B')
	default:
		return nil, errors.New("dbus: invalid byte order")
	}
	body := new(bytes.Buffer)
	fds = make([]int, 0)
	enc := newEncoder(body, order, fds)
	if len(msg.Body) != 0 {
		err = enc.Encode(msg.Body...)
		if err != nil {
			return
		}
	}
	vs[1] = msg.Type
	vs[2] = msg.Flags
	vs[3] = protoVersion
	vs[4] = uint32(len(body.Bytes()))
	vs[5] = msg.serial
	headers := make([]header, 0, len(msg.Headers))
	for k, v := range msg.Headers {
		headers = append(headers, header{byte(k), v})
	}
	vs[6] = headers
	var buf bytes.Buffer
	enc = newEncoder(&buf, order, enc.fds)
	err = enc.Encode(vs[:]...)
	if err != nil {
		return
	}
	enc.align(8)
	body.WriteTo(&buf)
	if buf.Len() > 1<<27 {
		return make([]int, 0), InvalidMessageError("message is too long")
	}
	if _, err := buf.WriteTo(out); err != nil {
		return make([]int, 0), err
	}
	return enc.fds, nil
}

// EncodeTo encodes and sends a message to the given writer. The byte order must
// be either binary.LittleEndian or binary.BigEndian. If the message is not
// valid or an error occurs when writing, an error is returned.
func (msg *Message) EncodeTo(out io.Writer, order binary.ByteOrder) (err error) {
	_, err = msg.EncodeToWithFDs(out, order)
	return err
}

// IsValid checks whether msg is a valid message and returns an
// InvalidMessageError or FormatError if it is not.
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

// Serial returns the message's serial number. The returned value is only valid
// for messages received by eavesdropping.
func (msg *Message) Serial() uint32 {
	return msg.serial
}

// String returns a string representation of a message similar to the format of
// dbus-monitor.
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
```

### Appendix G — `conn.go` (EXCERPT — not the full 996-line file)

This file is godbus's general-purpose `Conn`: object export, name
ownership, context-aware sends, interceptors, eavesdropping, unix-FD
negotiation. None of that is in scope (§1). What follows is only the
functions named throughout §4 — session-bus discovery, `Hello`, the read
loop, the send/`Call` chain, transport/address lookup, and serial
allocation — each still exactly as written upstream, so the state
transitions and lock ordering can be copied faithfully. `// ... cut ...`
marks where unrelated upstream code was removed for this excerpt; it is
not part of the source.

```go
package dbus

func SessionBus() (conn *Conn, err error) {
	sessionBusLck.Lock()
	defer sessionBusLck.Unlock()
	if sessionBus != nil &&
		sessionBus.Connected() {
		return sessionBus, nil
	}
	defer func() {
		if conn != nil {
			sessionBus = conn
		}
	}()
	conn, err = ConnectSessionBus()
	return
}

func getSessionBusAddress(autolaunch bool) (string, error) {
	if address := os.Getenv("DBUS_SESSION_BUS_ADDRESS"); address != "" && address != "autolaunch:" {
		return address, nil

	} else if address := tryDiscoverDbusSessionBusAddress(); address != "" {
		os.Setenv("DBUS_SESSION_BUS_ADDRESS", address)
		return address, nil
	}
	if !autolaunch {
		return "", errors.New("dbus: couldn't determine address of session bus")
	}
	return getSessionBusPlatformAddress()
}

// SessionBusPrivate returns a new private connection to the session bus.
func SessionBusPrivate(opts ...ConnOption) (*Conn, error) {
	address, err := getSessionBusAddress(true)
	if err != nil {
		return nil, err
	}

	return Dial(address, opts...)
}

// SessionBusPrivate returns a new private connection to the session bus.  If
// the session bus is not already open, do not attempt to launch it.

// ... cut: SystemBus, ConnectSessionBus/ConnectSystemBus, Connect, Dial,
// ConnOption family, NewConn — this module has one bus (session), one
// transport (unix), no options ...

func (conn *Conn) getSerial() uint32 {
	return conn.serialGen.GetSerial()
}

// Hello sends the initial org.freedesktop.DBus.Hello call. This method must be
// called after authentication, but before sending any other messages to the
// bus. Hello must not be called for shared connections.
func (conn *Conn) Hello() error {
	var s string
	err := conn.busObj.Call("org.freedesktop.DBus.Hello", 0).Store(&s)
	if err != nil {
		return err
	}
	conn.names.acquireUniqueConnectionName(s)
	return nil
}

// inWorker runs in an own goroutine, reading incoming messages from the
// transport and dispatching them appropriately.
func (conn *Conn) inWorker() {
	sequenceGen := newSequenceGenerator()
	for {
		msg, err := conn.ReadMessage()
		if err != nil {
			if _, ok := err.(InvalidMessageError); !ok {
				// Some read error occurred (usually EOF); we can't really do
				// anything but to shut down all stuff and returns errors to all
				// pending replies.
				conn.Close()
				conn.calls.finalizeAllWithError(sequenceGen, err)
				return
			}
			// invalid messages are ignored
			continue
		}
		conn.eavesdroppedLck.Lock()
		if conn.eavesdropped != nil {
			select {
			case conn.eavesdropped <- msg:
			default:
			}
			conn.eavesdroppedLck.Unlock()
			continue
		}
		conn.eavesdroppedLck.Unlock()
		dest, _ := msg.Headers[FieldDestination].value.(string)
		found := dest == "" ||
			!conn.names.uniqueNameIsKnown() ||
			conn.names.isKnownName(dest)
		if !found {
			// Eavesdropped a message, but no channel for it is registered.
			// Ignore it.
			continue
		}

		if conn.inInt != nil {
			conn.inInt(msg)
		}
		sequence := sequenceGen.next()
		switch msg.Type {
		case TypeError:
			conn.serialGen.RetireSerial(conn.calls.handleDBusError(sequence, msg))
		case TypeMethodReply:
			conn.serialGen.RetireSerial(conn.calls.handleReply(sequence, msg))
		case TypeSignal:
			conn.handleSignal(sequence, msg)
		case TypeMethodCall:
			go conn.handleCall(msg)
		}

	}
}

func (conn *Conn) handleSignal(sequence Sequence, msg *Message) {
	iface := msg.Headers[FieldInterface].value.(string)
	member := msg.Headers[FieldMember].value.(string)
	// as per http://dbus.freedesktop.org/doc/dbus-specification.html ,
	// sender is optional for signals.
	sender, _ := msg.Headers[FieldSender].value.(string)
	if iface == "org.freedesktop.DBus" && sender == "org.freedesktop.DBus" {
		if member == "NameLost" {
			// If we lost the name on the bus, remove it from our
			// tracking list.
			name, ok := msg.Body[0].(string)
			if !ok {
				panic("Unable to read the lost name")
			}
			conn.names.loseName(name)
		} else if member == "NameAcquired" {
			// If we acquired the name on the bus, add it to our
			// tracking list.
			name, ok := msg.Body[0].(string)
			if !ok {
				panic("Unable to read the acquired name")
			}
			conn.names.acquireName(name)
		}
	}
	signal := &Signal{
		Sender:   sender,
		Path:     msg.Headers[FieldPath].value.(ObjectPath),
		Name:     iface + "." + member,
		Body:     msg.Body,
		Sequence: sequence,
	}
	conn.signalHandler.DeliverSignal(iface, member, signal)
}

// Names returns the list of all names that are currently owned by this
// connection. The slice is always at least one element long, the first element
// being the unique name of the connection.

// ... cut: Names, Object, sendMessageAndIfClosed/handleSendError
// (this excerpt keeps the simpler send() below instead) ...

func (conn *Conn) Send(msg *Message, ch chan *Call) *Call {
	return conn.send(context.Background(), msg, ch)
}

// SendWithContext acts like Send but takes a context
func (conn *Conn) SendWithContext(ctx context.Context, msg *Message, ch chan *Call) *Call {
	return conn.send(ctx, msg, ch)
}

func (conn *Conn) send(ctx context.Context, msg *Message, ch chan *Call) *Call {
	if ctx == nil {
		panic("nil context")
	}
	if ch == nil {
		ch = make(chan *Call, 1)
	} else if cap(ch) == 0 {
		panic("dbus: unbuffered channel passed to (*Conn).Send")
	}

	var call *Call
	ctx, canceler := context.WithCancel(ctx)
	msg.serial = conn.getSerial()
	if msg.Type == TypeMethodCall && msg.Flags&FlagNoReplyExpected == 0 {
		call = new(Call)
		call.Destination, _ = msg.Headers[FieldDestination].value.(string)
		call.Path, _ = msg.Headers[FieldPath].value.(ObjectPath)
		iface, _ := msg.Headers[FieldInterface].value.(string)
		member, _ := msg.Headers[FieldMember].value.(string)
		call.Method = iface + "." + member
		call.Args = msg.Body
		call.Done = ch
		call.ctx = ctx
		call.ctxCanceler = canceler
		conn.calls.track(msg.serial, call)
		if ctx.Err() != nil {
			// short path: don't even send the message if context already cancelled
			conn.calls.handleSendError(msg, ctx.Err())
			return call
		}
		go func() {
			<-ctx.Done()
			conn.calls.handleSendError(msg, ctx.Err())
		}()
		conn.sendMessageAndIfClosed(msg, func() {
			conn.calls.handleSendError(msg, ErrClosed)
			canceler()
		})
	} else {
		canceler()
		call = &Call{Err: nil, Done: ch}
		ch <- call
		conn.sendMessageAndIfClosed(msg, func() {
			call = &Call{Err: ErrClosed}
		})
	}
	return call
}

// sendError creates an error message corresponding to the parameters and sends
// it to conn.out.

// ... cut: sendError, sendReply, AddMatchSignal*/RemoveMatchSignal*
// (this module's AddMatch/RemoveMatch in signal.go are the typed-rule
// equivalent), Signal/RemoveSignal (channel registration — port the
// registration shape, not these exact methods), SupportsUnixFDs ...

func NewError(name string, body []interface{}) *Error {
	return &Error{name, body}
}

func (e Error) Error() string {
	if len(e.Body) >= 1 {
		s, ok := e.Body[0].(string)
		if ok {
			return s
		}
	}
	return e.Name
}

// Signal represents a D-Bus message of type Signal. The name member is given in
// "interface.member" notation, e.g. org.freedesktop.D-Bus.NameLost.
type Signal struct {
	Sender   string
	Path     ObjectPath
	Name     string
	Body     []interface{}
	Sequence Sequence
}

// transport is a D-Bus transport.

// ... cut: transport interface declaration (this module's is narrower,
// unix-only) ...

func getTransport(address string) (transport, error) {
	var err error
	var t transport

	addresses := strings.Split(address, ";")
	for _, v := range addresses {
		i := strings.IndexRune(v, ':')
		if i == -1 {
			err = errors.New("dbus: invalid bus address (no transport)")
			continue
		}
		f := transports[v[:i]]
		if f == nil {
			err = errors.New("dbus: invalid bus address (invalid or unsupported transport)")
			continue
		}
		t, err = f(v[i+1:])
		if err == nil {
			return t, nil
		}
	}
	return nil, err
}

// getKey gets a key from a the list of keys. Returns "" on error / not found...
func getKey(s, key string) string {
	for _, keyEqualsValue := range strings.Split(s, ",") {
		keyValue := strings.SplitN(keyEqualsValue, "=", 2)
		if len(keyValue) == 2 && keyValue[0] == key {
			val, err := UnescapeBusAddressValue(keyValue[1])
			if err != nil {
				// No way to return an error.
				return ""
			}
			return val
		}
	}
	return ""
}

type outputHandler struct {
	conn    *Conn
	sendLck sync.Mutex
	closed  struct {
		isClosed bool
		lck      sync.RWMutex
	}
}

func (h *outputHandler) sendAndIfClosed(msg *Message, ifClosed func()) error {
	h.closed.lck.RLock()
	defer h.closed.lck.RUnlock()
	if h.closed.isClosed {
		if ifClosed != nil {
			ifClosed()
		}
		return nil
	}
	h.sendLck.Lock()
	defer h.sendLck.Unlock()
	return h.conn.SendMessage(msg)
}

func (h *outputHandler) close() {
	h.closed.lck.Lock()
	defer h.closed.lck.Unlock()
	h.closed.isClosed = true
}

type serialGenerator struct {
	lck        sync.Mutex
	nextSerial uint32
	serialUsed map[uint32]bool
}

func newSerialGenerator() *serialGenerator {
	return &serialGenerator{
		serialUsed: map[uint32]bool{0: true},
		nextSerial: 1,
	}
}

func (gen *serialGenerator) GetSerial() uint32 {
	gen.lck.Lock()
	defer gen.lck.Unlock()
	n := gen.nextSerial
	for gen.serialUsed[n] {
		n++
	}
	gen.serialUsed[n] = true
	gen.nextSerial = n + 1
	return n
}

func (gen *serialGenerator) RetireSerial(serial uint32) {
	gen.lck.Lock()
	defer gen.lck.Unlock()
	delete(gen.serialUsed, serial)
}

// ... cut: nameTracker (name ownership — out of scope) ...

type callTracker struct {
	calls map[uint32]*Call
	lck   sync.RWMutex
}

func newCallTracker() *callTracker {
	return &callTracker{calls: map[uint32]*Call{}}
}

func (tracker *callTracker) track(sn uint32, call *Call) {
	tracker.lck.Lock()
	tracker.calls[sn] = call
	tracker.lck.Unlock()
}

func (tracker *callTracker) handleReply(sequence Sequence, msg *Message) uint32 {
	serial := msg.Headers[FieldReplySerial].value.(uint32)
	tracker.lck.RLock()
	_, ok := tracker.calls[serial]
	tracker.lck.RUnlock()
	if ok {
		tracker.finalizeWithBody(serial, sequence, msg.Body)
	}
	return serial
}

func (tracker *callTracker) handleDBusError(sequence Sequence, msg *Message) uint32 {
	serial := msg.Headers[FieldReplySerial].value.(uint32)
	tracker.lck.RLock()
	_, ok := tracker.calls[serial]
	tracker.lck.RUnlock()
	if ok {
		name, _ := msg.Headers[FieldErrorName].value.(string)
		tracker.finalizeWithError(serial, sequence, Error{name, msg.Body})
	}
	return serial
}

func (tracker *callTracker) handleSendError(msg *Message, err error) {
	if err == nil {
		return
	}
	tracker.lck.RLock()
	_, ok := tracker.calls[msg.serial]
	tracker.lck.RUnlock()
	if ok {
		tracker.finalizeWithError(msg.serial, NoSequence, err)
	}
}

// finalize was the only func that did not strobe Done
func (tracker *callTracker) finalize(sn uint32) {
	tracker.lck.Lock()
	defer tracker.lck.Unlock()
	c, ok := tracker.calls[sn]
	if ok {
		delete(tracker.calls, sn)
		c.ContextCancel()
	}
}

func (tracker *callTracker) finalizeWithBody(sn uint32, sequence Sequence, body []interface{}) {
	tracker.lck.Lock()
	c, ok := tracker.calls[sn]
	if ok {
		delete(tracker.calls, sn)
	}
	tracker.lck.Unlock()
	if ok {
		c.Body = body
		c.ResponseSequence = sequence
		c.done()
	}
}

func (tracker *callTracker) finalizeWithError(sn uint32, sequence Sequence, err error) {
	tracker.lck.Lock()
	c, ok := tracker.calls[sn]
	if ok {
		delete(tracker.calls, sn)
	}
	tracker.lck.Unlock()
	if ok {
		c.Err = err
		c.ResponseSequence = sequence
		c.done()
	}
}

func (tracker *callTracker) finalizeAllWithError(sequenceGen *sequenceGenerator, err error) {
	tracker.lck.Lock()
	closedCalls := make([]*Call, 0, len(tracker.calls))
	for sn := range tracker.calls {
		closedCalls = append(closedCalls, tracker.calls[sn])
	}
	tracker.calls = map[uint32]*Call{}
	tracker.lck.Unlock()
	for _, call := range closedCalls {
		call.Err = err
		call.ResponseSequence = sequenceGen.next()
		call.done()
	}
}
```

### Appendix H — `transport_generic.go + transport_unix.go`

The dial + basic send/receive. Port narrowed: drop every unix-FD branch in transport_unix.go (CountFds, EnableUnixFDs, the oobReader control-message parsing, WriteMsgUnix/ReadMsgUnix) — this module never negotiates NEGOTIATE_UNIX_FD, so SendMessage/ReadMessage collapse to genericTransport's shape even inside unixTransport.

```go
package dbus

import (
	"encoding/binary"
	"errors"
	"io"
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

type genericTransport struct {
	io.ReadWriteCloser
}

func (t genericTransport) SendNullByte() error {
	_, err := t.Write([]byte{0})
	return err
}

func (t genericTransport) SupportsUnixFDs() bool {
	return false
}

func (t genericTransport) EnableUnixFDs() {}

func (t genericTransport) ReadMessage() (*Message, error) {
	return DecodeMessage(t)
}

func (t genericTransport) SendMessage(msg *Message) error {
	fds, err := msg.CountFds()
	if err != nil {
		return err
	}
	if fds != 0 {
		return errors.New("dbus: unix fd passing not enabled")
	}
	return msg.EncodeTo(t, nativeEndian)
}
```

`transport_unix.go` — the unix-socket dial and the FD-passing branches to prune
(§4's "Address parsing" already names what to keep: `newUnixTransport`'s dial
logic):

```go
//+build !windows,!solaris

package dbus

import (
	"bytes"
	"encoding/binary"
	"errors"
	"io"
	"net"
	"syscall"
)

type oobReader struct {
	conn *net.UnixConn
	oob  []byte
	buf  [4096]byte
}

func (o *oobReader) Read(b []byte) (n int, err error) {
	n, oobn, flags, _, err := o.conn.ReadMsgUnix(b, o.buf[:])
	if err != nil {
		return n, err
	}
	if flags&syscall.MSG_CTRUNC != 0 {
		return n, errors.New("dbus: control data truncated (too many fds received)")
	}
	o.oob = append(o.oob, o.buf[:oobn]...)
	return n, nil
}

type unixTransport struct {
	*net.UnixConn
	rdr        *oobReader
	hasUnixFDs bool
}

func newUnixTransport(keys string) (transport, error) {
	var err error

	t := new(unixTransport)
	abstract := getKey(keys, "abstract")
	path := getKey(keys, "path")
	switch {
	case abstract == "" && path == "":
		return nil, errors.New("dbus: invalid address (neither path nor abstract set)")
	case abstract != "" && path == "":
		t.UnixConn, err = net.DialUnix("unix", nil, &net.UnixAddr{Name: "@" + abstract, Net: "unix"})
		if err != nil {
			return nil, err
		}
		return t, nil
	case abstract == "" && path != "":
		t.UnixConn, err = net.DialUnix("unix", nil, &net.UnixAddr{Name: path, Net: "unix"})
		if err != nil {
			return nil, err
		}
		return t, nil
	default:
		return nil, errors.New("dbus: invalid address (both path and abstract set)")
	}
}

func init() {
	transports["unix"] = newUnixTransport
}

func (t *unixTransport) EnableUnixFDs() {
	t.hasUnixFDs = true
}

func (t *unixTransport) ReadMessage() (*Message, error) {
	var (
		blen, hlen uint32
		csheader   [16]byte
		headers    []header
		order      binary.ByteOrder
		unixfds    uint32
	)
	// To be sure that all bytes of out-of-band data are read, we use a special
	// reader that uses ReadUnix on the underlying connection instead of Read
	// and gathers the out-of-band data in a buffer.
	if t.rdr == nil {
		t.rdr = &oobReader{conn: t.UnixConn}
	} else {
		t.rdr.oob = nil
	}

	// read the first 16 bytes (the part of the header that has a constant size),
	// from which we can figure out the length of the rest of the message
	if _, err := io.ReadFull(t.rdr, csheader[:]); err != nil {
		return nil, err
	}
	switch csheader[0] {
	case 'l':
		order = binary.LittleEndian
	case 'B':
		order = binary.BigEndian
	default:
		return nil, InvalidMessageError("invalid byte order")
	}
	// csheader[4:8] -> length of message body, csheader[12:16] -> length of
	// header fields (without alignment)
	binary.Read(bytes.NewBuffer(csheader[4:8]), order, &blen)
	binary.Read(bytes.NewBuffer(csheader[12:]), order, &hlen)
	if hlen%8 != 0 {
		hlen += 8 - (hlen % 8)
	}

	// decode headers and look for unix fds
	headerdata := make([]byte, hlen+4)
	copy(headerdata, csheader[12:])
	if _, err := io.ReadFull(t.rdr, headerdata[4:]); err != nil {
		return nil, err
	}
	dec := newDecoder(bytes.NewBuffer(headerdata), order, make([]int, 0))
	dec.pos = 12
	vs, err := dec.Decode(Signature{"a(yv)"})
	if err != nil {
		return nil, err
	}
	Store(vs, &headers)
	for _, v := range headers {
		if v.Field == byte(FieldUnixFDs) {
			unixfds, _ = v.Variant.value.(uint32)
		}
	}
	all := make([]byte, 16+hlen+blen)
	copy(all, csheader[:])
	copy(all[16:], headerdata[4:])
	if _, err := io.ReadFull(t.rdr, all[16+hlen:]); err != nil {
		return nil, err
	}
	if unixfds != 0 {
		if !t.hasUnixFDs {
			return nil, errors.New("dbus: got unix fds on unsupported transport")
		}
		// read the fds from the OOB data
		scms, err := syscall.ParseSocketControlMessage(t.rdr.oob)
		if err != nil {
			return nil, err
		}
		if len(scms) != 1 {
			return nil, errors.New("dbus: received more than one socket control message")
		}
		fds, err := syscall.ParseUnixRights(&scms[0])
		if err != nil {
			return nil, err
		}
		msg, err := DecodeMessageWithFDs(bytes.NewBuffer(all), fds)
		if err != nil {
			return nil, err
		}
		// substitute the values in the message body (which are indices for the
		// array receiver via OOB) with the actual values
		for i, v := range msg.Body {
			switch index := v.(type) {
			case UnixFDIndex:
				if uint32(index) >= unixfds {
					return nil, InvalidMessageError("invalid index for unix fd")
				}
				msg.Body[i] = UnixFD(fds[index])
			case []UnixFDIndex:
				fdArray := make([]UnixFD, len(index))
				for k, j := range index {
					if uint32(j) >= unixfds {
						return nil, InvalidMessageError("invalid index for unix fd")
					}
					fdArray[k] = UnixFD(fds[j])
				}
				msg.Body[i] = fdArray
			}
		}
		return msg, nil
	}
	return DecodeMessage(bytes.NewBuffer(all))
}

func (t *unixTransport) SendMessage(msg *Message) error {
	fdcnt, err := msg.CountFds()
	if err != nil {
		return err
	}
	if fdcnt != 0 {
		if !t.hasUnixFDs {
			return errors.New("dbus: unix fd passing not enabled")
		}
		msg.Headers[FieldUnixFDs] = MakeVariant(uint32(fdcnt))
		buf := new(bytes.Buffer)
		fds, err := msg.EncodeToWithFDs(buf, nativeEndian)
		if err != nil {
			return err
		}
		oob := syscall.UnixRights(fds...)
		n, oobn, err := t.UnixConn.WriteMsgUnix(buf.Bytes(), oob, nil)
		if err != nil {
			return err
		}
		if n != buf.Len() || oobn != len(oob) {
			return io.ErrShortWrite
		}
	} else {
		if err := msg.EncodeTo(t, nativeEndian); err != nil {
			return err
		}
	}
	return nil
}

func (t *unixTransport) SupportsUnixFDs() bool {
	return true
}
```

### Appendix K — `sig_test.go` (test source, port into `tests/`)

Port unchanged into tests/sig_test.go — fully in §4's scope.

```go
package dbus

import (
	"testing"
)

var sigTests = []struct {
	vs  []interface{}
	sig Signature
}{
	{
		[]interface{}{new(int32)},
		Signature{"i"},
	},
	{
		[]interface{}{new(string)},
		Signature{"s"},
	},
	{
		[]interface{}{new(Signature)},
		Signature{"g"},
	},
	{
		[]interface{}{new([]int16)},
		Signature{"an"},
	},
	{
		[]interface{}{new(int16), new(uint32)},
		Signature{"nu"},
	},
	{
		[]interface{}{new(map[byte]Variant)},
		Signature{"a{yv}"},
	},
	{
		[]interface{}{new(Variant), new([]map[int32]string)},
		Signature{"vaa{is}"},
	},
}

func TestSig(t *testing.T) {
	for i, v := range sigTests {
		sig := SignatureOf(v.vs...)
		if sig != v.sig {
			t.Errorf("test %d: got %q, expected %q", i+1, sig.str, v.sig.str)
		}
	}
}

var getSigTest = []interface{}{
	[]struct {
		b byte
		i int32
		t uint64
		s string
	}{},
	map[string]Variant{},
}

func BenchmarkGetSignatureSimple(b *testing.B) {
	for i := 0; i < b.N; i++ {
		SignatureOf("", int32(0))
	}
}

func BenchmarkGetSignatureLong(b *testing.B) {
	for i := 0; i < b.N; i++ {
		SignatureOf(getSigTest...)
	}
}
```

### Appendix L — `variant_test.go` (test source, port into `tests/`)

Port into tests/variant_test.go, pruning any case for a type outside §4's set.

```go
package dbus

import "reflect"
import "testing"

var variantFormatTests = []struct {
	v interface{}
	s string
}{
	{int32(1), `1`},
	{"foo", `"foo"`},
	{ObjectPath("/org/foo"), `@o "/org/foo"`},
	{Signature{"i"}, `@g "i"`},
	{[]byte{}, `@ay []`},
	{[]int32{1, 2}, `[1, 2]`},
	{[]int64{1, 2}, `@ax [1, 2]`},
	{[][]int32{{3, 4}, {5, 6}}, `[[3, 4], [5, 6]]`},
	{[]Variant{MakeVariant(int32(1)), MakeVariant(1.0)}, `[<1>, <@d 1>]`},
	{map[string]int32{"one": 1, "two": 2}, `{"one": 1, "two": 2}`},
	{map[int32]ObjectPath{1: "/org/foo"}, `@a{io} {1: "/org/foo"}`},
	{map[string]Variant{}, `@a{sv} {}`},
}

func TestFormatVariant(t *testing.T) {
	for i, v := range variantFormatTests {
		if s := MakeVariant(v.v).String(); s != v.s {
			t.Errorf("test %d: got %q, wanted %q", i+1, s, v.s)
		}
	}
}

var variantParseTests = []struct {
	s string
	v interface{}
}{
	{"1", int32(1)},
	{"true", true},
	{"false", false},
	{"1.0", float64(1.0)},
	{"0x10", int32(16)},
	{"1e1", float64(10)},
	{`"foo"`, "foo"},
	{`"\a\b\f\n\r\t"`, "\x07\x08\x0c\n\r\t"},
	{`"\u00e4\U0001f603"`, "\u00e4\U0001f603"},
	{"[1]", []int32{1}},
	{"[1, 2, 3]", []int32{1, 2, 3}},
	{"@ai []", []int32{}},
	{"[1, 5.0]", []float64{1, 5.0}},
	{"[[1, 2], [3, 4.0]]", [][]float64{{1, 2}, {3, 4}}},
	{`[@o "/org/foo", "/org/bar"]`, []ObjectPath{"/org/foo", "/org/bar"}},
	{"<1>", MakeVariant(int32(1))},
	{"[<1>, <2.0>]", []Variant{MakeVariant(int32(1)), MakeVariant(2.0)}},
	{`[[], [""]]`, [][]string{{}, {""}}},
	{`@a{ss} {}`, map[string]string{}},
	{`{"foo": 1}`, map[string]int32{"foo": 1}},
	{`[{}, {"foo": "bar"}]`, []map[string]string{{}, {"foo": "bar"}}},
	{`{"a": <1>, "b": <"foo">}`,
		map[string]Variant{"a": MakeVariant(int32(1)), "b": MakeVariant("foo")}},
	{`b''`, []byte{0}},
	{`b"abc"`, []byte{'a', 'b', 'c', 0}},
	{`b"\x01\0002\a\b\f\n\r\t"`, []byte{1, 2, 0x7, 0x8, 0xc, '\n', '\r', '\t', 0}},
	{`[[0], b""]`, [][]byte{{0}, {0}}},
	{"int16 0", int16(0)},
	{"byte 0", byte(0)},
}

func TestParseVariant(t *testing.T) {
	for i, v := range variantParseTests {
		nv, err := ParseVariant(v.s, Signature{})
		if err != nil {
			t.Errorf("test %d: parsing failed: %s", i+1, err)
			continue
		}
		if !reflect.DeepEqual(nv.value, v.v) {
			t.Errorf("test %d: got %q, wanted %q", i+1, nv, v.v)
		}
	}
}

func TestVariantStore(t *testing.T) {
	str := "foo bar"
	v := MakeVariant(str)
	var result string
	err := v.Store(&result)
	if err != nil {
		t.Fatal(err)
	}
	if result != str {
		t.Fatalf("expected %s, got %s\n", str, result)
	}

}
```

### Appendix M — `encoder_test.go` (test source, port into `tests/`)

Port into tests/encoder_test.go, pruning struct/reflection-driven cases per §8's table.

```go
package dbus

import (
	"bytes"
	"encoding/binary"
	"reflect"
	"testing"
)

func TestEncodeArrayOfMaps(t *testing.T) {
	tests := []struct {
		name string
		vs   []interface{}
	}{
		{
			"aligned at 8 at start of array",
			[]interface{}{
				"12345",
				[]map[string]Variant{
					{
						"abcdefg": MakeVariant("foo"),
						"cdef":    MakeVariant(uint32(2)),
					},
				},
			},
		},
		{
			"not aligned at 8 for start of array",
			[]interface{}{
				"1234567890",
				[]map[string]Variant{
					{
						"abcdefg": MakeVariant("foo"),
						"cdef":    MakeVariant(uint32(2)),
					},
				},
			},
		},
	}
	for _, order := range []binary.ByteOrder{binary.LittleEndian, binary.BigEndian} {
		for _, tt := range tests {
			buf := new(bytes.Buffer)
			fds := make([]int, 0)
			enc := newEncoder(buf, order, fds)
			enc.Encode(tt.vs...)

			dec := newDecoder(buf, order, enc.fds)
			v, err := dec.Decode(SignatureOf(tt.vs...))
			if err != nil {
				t.Errorf("%q: decode (%v) failed: %v", tt.name, order, err)
				continue
			}
			if !reflect.DeepEqual(v, tt.vs) {
				t.Errorf("%q: (%v) not equal: got '%v', want '%v'", tt.name, order, v, tt.vs)
				continue
			}
		}
	}
}

func TestEncodeMapStringInterface(t *testing.T) {
	val := map[string]interface{}{"foo": "bar"}
	buf := new(bytes.Buffer)
	fds := make([]int, 0)
	order := binary.LittleEndian
	enc := newEncoder(buf, binary.LittleEndian, fds)
	err := enc.Encode(val)
	if err != nil {
		t.Fatal(err)
	}

	dec := newDecoder(buf, order, enc.fds)
	v, err := dec.Decode(SignatureOf(val))
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]interface{}{}
	Store(v, &out)
	if !reflect.DeepEqual(out, val) {
		t.Errorf("not equal: got '%v', want '%v'",
			out, val)
	}
}

type empty interface{}

func TestEncodeMapStringNamedInterface(t *testing.T) {
	val := map[string]empty{"foo": "bar"}
	buf := new(bytes.Buffer)
	fds := make([]int, 0)
	order := binary.LittleEndian
	enc := newEncoder(buf, binary.LittleEndian, fds)
	err := enc.Encode(val)
	if err != nil {
		t.Fatal(err)
	}

	dec := newDecoder(buf, order, enc.fds)
	v, err := dec.Decode(SignatureOf(val))
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]empty{}
	Store(v, &out)
	if !reflect.DeepEqual(out, val) {
		t.Errorf("not equal: got '%v', want '%v'",
			out, val)
	}
}

type fooer interface {
	Foo()
}

type fooimpl string

func (fooimpl) Foo() {}

func TestEncodeMapStringNonEmptyInterface(t *testing.T) {
	val := map[string]fooer{"foo": fooimpl("bar")}
	buf := new(bytes.Buffer)
	fds := make([]int, 0)
	order := binary.LittleEndian
	enc := newEncoder(buf, binary.LittleEndian, fds)
	err := enc.Encode(val)
	if err != nil {
		t.Fatal(err)
	}

	dec := newDecoder(buf, order, enc.fds)
	v, err := dec.Decode(SignatureOf(val))
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]fooer{}
	err = Store(v, &out)
	if err == nil {
		t.Fatal("Shouldn't be able to convert to non empty interfaces")
	}
}

func TestEncodeSliceInterface(t *testing.T) {
	val := []interface{}{"foo", "bar"}
	buf := new(bytes.Buffer)
	fds := make([]int, 0)
	order := binary.LittleEndian
	enc := newEncoder(buf, binary.LittleEndian, fds)
	err := enc.Encode(val)
	if err != nil {
		t.Fatal(err)
	}

	dec := newDecoder(buf, order, enc.fds)
	v, err := dec.Decode(SignatureOf(val))
	if err != nil {
		t.Fatal(err)
	}
	out := []interface{}{}
	Store(v, &out)
	if !reflect.DeepEqual(out, val) {
		t.Errorf("not equal: got '%v', want '%v'",
			out, val)
	}
}

func TestEncodeSliceNamedInterface(t *testing.T) {
	val := []empty{"foo", "bar"}
	buf := new(bytes.Buffer)
	fds := make([]int, 0)
	order := binary.LittleEndian
	enc := newEncoder(buf, binary.LittleEndian, fds)
	err := enc.Encode(val)
	if err != nil {
		t.Fatal(err)
	}

	dec := newDecoder(buf, order, enc.fds)
	v, err := dec.Decode(SignatureOf(val))
	if err != nil {
		t.Fatal(err)
	}
	out := []empty{}
	Store(v, &out)
	if !reflect.DeepEqual(out, val) {
		t.Errorf("not equal: got '%v', want '%v'",
			out, val)
	}
}

func TestEncodeNestedInterface(t *testing.T) {
	val := map[string]interface{}{
		"foo": []interface{}{"1", "2", "3", "5",
			map[string]interface{}{
				"bar": "baz",
			},
		},
		"bar": map[string]interface{}{
			"baz":  "quux",
			"quux": "quuz",
		},
	}
	buf := new(bytes.Buffer)
	fds := make([]int, 0)
	order := binary.LittleEndian
	enc := newEncoder(buf, binary.LittleEndian, fds)
	err := enc.Encode(val)
	if err != nil {
		t.Fatal(err)
	}

	dec := newDecoder(buf, order, enc.fds)
	v, err := dec.Decode(SignatureOf(val))
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]interface{}{}
	Store(v, &out)
	if !reflect.DeepEqual(out, val) {
		t.Errorf("not equal: got '%#v', want '%#v'",
			out, val)
	}
}

func TestEncodeInt(t *testing.T) {
	val := 10
	buf := new(bytes.Buffer)
	fds := make([]int, 0)
	order := binary.LittleEndian
	enc := newEncoder(buf, binary.LittleEndian, fds)
	err := enc.Encode(val)
	if err != nil {
		t.Fatal(err)
	}

	dec := newDecoder(buf, order, enc.fds)
	v, err := dec.Decode(SignatureOf(val))
	if err != nil {
		t.Fatal(err)
	}
	var out int
	Store(v, &out)
	if !reflect.DeepEqual(out, val) {
		t.Errorf("not equal: got '%v', want '%v'",
			out, val)
	}
}

func TestEncodeIntToNonCovertible(t *testing.T) {
	val := 150
	buf := new(bytes.Buffer)
	fds := make([]int, 0)
	order := binary.LittleEndian
	enc := newEncoder(buf, binary.LittleEndian, fds)
	err := enc.Encode(val)
	if err != nil {
		t.Fatal(err)
	}

	dec := newDecoder(buf, order, enc.fds)
	v, err := dec.Decode(SignatureOf(val))
	if err != nil {
		t.Fatal(err)
	}
	var out bool
	err = Store(v, &out)
	if err == nil {
		t.Logf("%t\n", out)
		t.Fatal("Type mismatch should have occurred")
	}
}

func TestEncodeUint(t *testing.T) {
	val := uint(10)
	buf := new(bytes.Buffer)
	fds := make([]int, 0)
	order := binary.LittleEndian
	enc := newEncoder(buf, binary.LittleEndian, fds)
	err := enc.Encode(val)
	if err != nil {
		t.Fatal(err)
	}

	dec := newDecoder(buf, order, enc.fds)
	v, err := dec.Decode(SignatureOf(val))
	if err != nil {
		t.Fatal(err)
	}
	var out uint
	Store(v, &out)
	if !reflect.DeepEqual(out, val) {
		t.Errorf("not equal: got '%v', want '%v'",
			out, val)
	}
}

func TestEncodeUintToNonCovertible(t *testing.T) {
	val := uint(10)
	buf := new(bytes.Buffer)
	fds := make([]int, 0)
	order := binary.LittleEndian
	enc := newEncoder(buf, binary.LittleEndian, fds)
	err := enc.Encode(val)
	if err != nil {
		t.Fatal(err)
	}

	dec := newDecoder(buf, order, enc.fds)
	v, err := dec.Decode(SignatureOf(val))
	if err != nil {
		t.Fatal(err)
	}
	var out bool
	err = Store(v, &out)
	if err == nil {
		t.Fatal("Type mismatch should have occurred")
	}
}

type boolean bool

func TestEncodeOfAssignableType(t *testing.T) {
	val := boolean(true)
	buf := new(bytes.Buffer)
	fds := make([]int, 0)
	order := binary.LittleEndian
	enc := newEncoder(buf, binary.LittleEndian, fds)
	err := enc.Encode(val)
	if err != nil {
		t.Fatal(err)
	}

	dec := newDecoder(buf, order, enc.fds)
	v, err := dec.Decode(SignatureOf(val))
	if err != nil {
		t.Fatal(err)
	}
	var out boolean
	err = Store(v, &out)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(out, val) {
		t.Errorf("not equal: got '%v', want '%v'",
			out, val)
	}
}

func TestEncodeVariant(t *testing.T) {
	var res map[ObjectPath]map[string]map[string]Variant
	var src = map[ObjectPath]map[string]map[string]Variant{
		ObjectPath("/foo/bar"): {
			"foo": {
				"bar": MakeVariant(10),
				"baz": MakeVariant("20"),
			},
		},
	}
	buf := new(bytes.Buffer)
	fds := make([]int, 0)
	order := binary.LittleEndian
	enc := newEncoder(buf, binary.LittleEndian, fds)
	err := enc.Encode(src)
	if err != nil {
		t.Fatal(err)
	}

	dec := newDecoder(buf, order, enc.fds)
	v, err := dec.Decode(SignatureOf(src))
	if err != nil {
		t.Fatal(err)
	}
	err = Store(v, &res)
	if err != nil {
		t.Fatal(err)
	}
	_ = res[ObjectPath("/foo/bar")]["foo"]["baz"].Value().(string)
}

func TestEncodeVariantToList(t *testing.T) {
	var res map[string]Variant
	var src = map[string]interface{}{
		"foo": []interface{}{"a", "b", "c"},
	}
	buf := new(bytes.Buffer)
	fds := make([]int, 0)
	order := binary.LittleEndian
	enc := newEncoder(buf, binary.LittleEndian, fds)
	err := enc.Encode(src)
	if err != nil {
		t.Fatal(err)
	}

	dec := newDecoder(buf, order, enc.fds)
	v, err := dec.Decode(SignatureOf(src))
	if err != nil {
		t.Fatal(err)
	}
	err = Store(v, &res)
	if err != nil {
		t.Fatal(err)
	}
	_ = res["foo"].Value().([]Variant)
}

func TestEncodeVariantToUint64(t *testing.T) {
	var res map[string]Variant
	var src = map[string]interface{}{
		"foo": uint64(10),
	}
	buf := new(bytes.Buffer)
	fds := make([]int, 0)
	order := binary.LittleEndian
	enc := newEncoder(buf, binary.LittleEndian, fds)
	err := enc.Encode(src)
	if err != nil {
		t.Fatal(err)
	}

	dec := newDecoder(buf, order, enc.fds)
	v, err := dec.Decode(SignatureOf(src))
	if err != nil {
		t.Fatal(err)
	}
	err = Store(v, &res)
	if err != nil {
		t.Fatal(err)
	}
	_ = res["foo"].Value().(uint64)
}
```

### Appendix N — `decoder_test.go` (test source, port into `tests/`)

Port into tests/decoder_test.go, same pruning as Appendix M.

```go
package dbus

import (
	"bytes"
	"encoding/binary"
	"testing"
)

type pixmap struct {
	Width  int
	Height int
	Pixels []uint8
}

type property struct {
	IconName    string
	Pixmaps     []pixmap
	Title       string
	Description string
}

func TestDecodeArrayEmptyStruct(t *testing.T) {
	buf := bytes.NewBuffer(nil)
	msg := &Message{
		Type:  0x02,
		Flags: 0x00,
		Headers: map[HeaderField]Variant{
			0x06: Variant{
				sig: Signature{
					str: "s",
				},
				value: ":1.391",
			},
			0x05: Variant{
				sig: Signature{
					str: "u",
				},
				value: uint32(2),
			},
			0x08: Variant{
				sig: Signature{
					str: "g",
				},
				value: Signature{
					str: "v",
				},
			},
		},
		Body: []interface{}{
			Variant{
				sig: Signature{
					str: "(sa(iiay)ss)",
				},
				value: property{
					IconName:    "iconname",
					Pixmaps:     []pixmap{},
					Title:       "title",
					Description: "description",
				},
			},
		},
		serial: 0x00000003,
	}
	err := msg.EncodeTo(buf, binary.LittleEndian)
	if err != nil {
		t.Fatal(err)
	}
	msg, err = DecodeMessage(buf)
	if err != nil {
		t.Fatal(err)
	}
}

func TestSigByteSize(t *testing.T) {
	for sig, want := range map[string]int{
		"b":       4,
		"t":       8,
		"(yy)":    2,
		"(y(uu))": 9,
		"(y(xs))": 0,
		"s":       0,
		"ao":      0,
	} {
		if have := sigByteSize(sig); have != want {
			t.Errorf("sigByteSize(%q) = %d, want %d", sig, have, want)
		}
	}
}
```

### Appendix O — `message_test.go` (test source, port into `tests/`)

Port into tests/message_test.go, pruning UnixFD/UnixFDIndex cases — §1 excludes FD passing.

```go
package dbus

import "testing"

func TestMessage_validateHeader(t *testing.T) {
	var tcs = []struct {
		msg Message
		err error
	}{
		{
			msg: Message{
				Flags: 0xFF,
			},
			err: InvalidMessageError("invalid flags"),
		},
		{
			msg: Message{
				Type: 0xFF,
			},
			err: InvalidMessageError("invalid message type"),
		},
		{
			msg: Message{
				Type: TypeMethodCall,
				Headers: map[HeaderField]Variant{
					0xFF: MakeVariant("foo"),
				},
			},
			err: InvalidMessageError("invalid header"),
		},
		{
			msg: Message{
				Type: TypeMethodCall,
				Headers: map[HeaderField]Variant{
					FieldPath: MakeVariant(42),
				},
			},
			err: InvalidMessageError("invalid type of header field"),
		},
		{
			msg: Message{
				Type: TypeMethodCall,
			},
			err: InvalidMessageError("missing required header"),
		},
		{
			msg: Message{
				Type: TypeMethodCall,
				Headers: map[HeaderField]Variant{
					FieldPath:   MakeVariant(ObjectPath("break")),
					FieldMember: MakeVariant("foo.bar"),
				},
			},
			err: InvalidMessageError("invalid path name"),
		},
		{
			msg: Message{
				Type: TypeMethodCall,
				Headers: map[HeaderField]Variant{
					FieldPath:   MakeVariant(ObjectPath("/")),
					FieldMember: MakeVariant("2"),
				},
			},
			err: InvalidMessageError("invalid member name"),
		},
		{
			msg: Message{
				Type: TypeMethodCall,
				Headers: map[HeaderField]Variant{
					FieldPath:      MakeVariant(ObjectPath("/")),
					FieldMember:    MakeVariant("foo.bar"),
					FieldInterface: MakeVariant("break"),
				},
			},
			err: InvalidMessageError("invalid interface name"),
		},
		{
			msg: Message{
				Type: TypeError,
				Headers: map[HeaderField]Variant{
					FieldReplySerial: MakeVariant(uint32(0)),
					FieldErrorName:   MakeVariant("break"),
				},
			},
			err: InvalidMessageError("invalid error name"),
		},
		{

			msg: Message{
				Type: TypeError,
				Headers: map[HeaderField]Variant{
					FieldReplySerial: MakeVariant(uint32(0)),
					FieldErrorName:   MakeVariant("error.name"),
				},
				Body: []interface{}{
					"break",
				},
			},
			err: InvalidMessageError("missing signature"),
		},
	}

	for _, tc := range tcs {
		t.Run(tc.err.Error(), func(t *testing.T) {
			err := tc.msg.validateHeader()
			if err != tc.err {
				t.Errorf("expected error %q, got %q", tc.err, err)
			}
		})
	}
}
```
