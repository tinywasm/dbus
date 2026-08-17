# Architecture

## Overview

`tinywasm/dbus` is a synchronous/asynchronous hybrid D-Bus client designed around a single read loop goroutine per connection.

## Socket Ownership & Read Loop

```
                     +-----------------------+
                     |  Unix Socket Server   |
                     +-----------+-----------+
                                 |
                                 | Unix socket stream
                                 v
                     +-----------+-----------+
                     |     inWorker()        |  (Single background goroutine)
                     |    (Read Loop)        |
                     +-----+-----------+-----+
                           |           |
            TypeMethodReply|           |TypeSignal
               / TypeError |           |
                           v           v
           +---------------+--+     +--+---------------+
           | Pending Calls    |     | Signal Handler   |
           | map[serial]chan  |     | deliver to chans |
           +------------------+     +------------------+
```

1. **Connection Initialization:**
   `SessionBus()` parses `$DBUS_SESSION_BUS_ADDRESS` (or falls back to `$XDG_RUNTIME_DIR/bus`), dials the Unix domain socket, performs the `AUTH EXTERNAL` SASL handshake, spawns `inWorker()`, and sends `org.freedesktop.DBus.Hello`.

2. **Method Call Routing (`Call`):**
   - Each call allocates a monotonic serial integer.
   - The pending call registers a buffered channel in `calls map[uint32]chan *Reply` guarded by a mutex.
   - The encoded message is written to the socket under a write lock.
   - The caller blocks on the reply channel or a 30-second timeout.
   - When `inWorker()` reads a `TypeMethodReply` or `TypeError`, it retrieves the matching serial from `calls`, unregisters it, and delivers the `Reply` struct to the waiting channel.

3. **Signal Dispatching (`Signals` / `AddMatch`):**
   - Callers register signal receiver channels using `Conn.Signals(ch)`.
   - When `inWorker()` decodes a `TypeSignal` message, it builds a `Signal` struct containing sender, path, signal name, and body arguments, and non-blocking delivers it to all registered signal channels.

4. **Shutdown & Cleanup:**
   - Closing the connection (`Conn.Close()`) shuts down the socket reader and terminates all pending calls with `ErrClosed`.
