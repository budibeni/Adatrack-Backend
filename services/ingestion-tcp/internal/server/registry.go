package server

import (
	"net"
	"sync"
)

var (
	ConnRegistry sync.Map
)

func RegisterConnection(imei string, conn net.Conn) {
	ConnRegistry.Store(imei, conn)
}

func UnregisterConnection(imei string) {
	ConnRegistry.Delete(imei)
}

func SendCommand(imei string, data []byte) bool {
	if val, ok := ConnRegistry.Load(imei); ok {
		if conn, ok := val.(net.Conn); ok {
			_, err := conn.Write(data)
			return err == nil
		}
	}
	return false
}
