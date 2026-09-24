package controllers

// registry.go — live device connection registry (B8, PRD §21.2 row 1).
//
// Downlink ("server → device") commands can only be delivered over a connection
// the DEVICE opened, so ingestion-tcp keeps the accepted connections indexed by
// IMEI. The registry is deliberately small and lock-light: one map guarded by an
// RWMutex, bounded by TCP_MAX_CONNECTIONS (the accept budget), with writes
// serialised per connection so two concurrent commands can never interleave their
// bytes on the wire (a half-written frame would corrupt the device protocol).

import (
	"errors"
	"net"
	"sync"
	"time"

	"adatrack_gps/ingestion-tcp/models"
)

// ErrDeviceOffline is returned when a command targets an IMEI that currently has
// no live connection. It is a normal, expected outcome (device out of coverage)
// and callers report it as `offline` rather than as a failure.
var ErrDeviceOffline = errors.New("device is not connected")

// DeviceConn is one live device connection.
type DeviceConn struct {
	IMEI        string
	Protocol    models.Protocol
	Remote      string
	ConnectedAt time.Time

	conn net.Conn
	mu   sync.Mutex
}

// Write sends one full response/command frame, serialised per connection.
func (d *DeviceConn) Write(frame []byte) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	_, err := d.conn.Write(frame)
	return err
}

// Close closes the underlying connection (idempotent at the net layer).
func (d *DeviceConn) Close() error {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.conn.Close()
}

// ConnRegistry indexes live connections by IMEI.
type ConnRegistry struct {
	mu    sync.RWMutex
	conns map[string]*DeviceConn
	max   int
}

// NewConnRegistry builds a registry bounded by max (TCP_MAX_CONNECTIONS).
func NewConnRegistry(max int) *ConnRegistry {
	if max <= 0 {
		max = 5000
	}
	return &ConnRegistry{conns: make(map[string]*DeviceConn, 64), max: max}
}

// Add registers (or replaces) the connection of one IMEI. A reconnecting device
// closes the old socket first, so the newest connection always wins.
func (r *ConnRegistry) Add(d *DeviceConn) {
	if d == nil || d.IMEI == "" {
		return
	}
	r.mu.Lock()
	r.conns[d.IMEI] = d
	r.mu.Unlock()
}

// Remove unregisters d, but only when it is still the registered connection:
// during a reconnect the older handler must not evict the newer socket.
func (r *ConnRegistry) Remove(d *DeviceConn) {
	if d == nil || d.IMEI == "" {
		return
	}
	r.mu.Lock()
	if cur, ok := r.conns[d.IMEI]; ok && cur == d {
		delete(r.conns, d.IMEI)
	}
	r.mu.Unlock()
}

// Get returns the live connection of an IMEI.
func (r *ConnRegistry) Get(imei string) (*DeviceConn, bool) {
	r.mu.RLock()
	d, ok := r.conns[imei]
	r.mu.RUnlock()
	return d, ok
}

// Len reports how many devices are connected right now (metric + tests).
func (r *ConnRegistry) Len() int {
	r.mu.RLock()
	n := len(r.conns)
	r.mu.RUnlock()
	return n
}

// Online reports whether an IMEI currently holds a connection.
func (r *ConnRegistry) Online(imei string) bool {
	_, ok := r.Get(imei)
	return ok
}
