// Portions Copyright (c) 2013, Georg Reinke, Google — BSD-2-Clause

package dbus

import "sync"

type Signal struct {
	Sender string
	Path   ObjectPath
	Name   string // "interface.Member"
	Body   []any
}

type signalHandler struct {
	mu       sync.RWMutex
	channels []chan<- *Signal
}

func (sh *signalHandler) addChannel(ch chan<- *Signal) {
	sh.mu.Lock()
	defer sh.mu.Unlock()
	sh.channels = append(sh.channels, ch)
}

func (sh *signalHandler) deliver(sig *Signal) {
	sh.mu.RLock()
	defer sh.mu.RUnlock()
	for _, ch := range sh.channels {
		select {
		case ch <- sig:
		default:
			// Full channel drops signal rather than blocking read loop
		}
	}
}

func (c *Conn) Signals(ch chan<- *Signal) {
	c.signals.addChannel(ch)
}

func (c *Conn) AddMatch(rule string) error {
	obj := c.Object(busName, busPath)
	reply := obj.Call(methodAddMatch, rule)
	return reply.Err
}

func (c *Conn) RemoveMatch(rule string) error {
	obj := c.Object(busName, busPath)
	reply := obj.Call(methodRemoveMatch, rule)
	return reply.Err
}
