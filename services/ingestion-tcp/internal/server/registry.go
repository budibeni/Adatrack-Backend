package server

import (
	"net"
	"sync"
	"backend/ingestion-tcp/internal/protocol"
)

type DeviceConn struct {
	Conn    net.Conn
	Decoder protocol.Decoder
}

var (
	ConnRegistry sync.Map
)

func RegisterConnection(imei string, conn net.Conn, decoder protocol.Decoder) {
	ConnRegistry.Store(imei, DeviceConn{Conn: conn, Decoder: decoder})
}

func UnregisterConnection(imei string) {
	ConnRegistry.Delete(imei)
}

func SendCommand(imei string, data []byte) bool {
	if val, ok := ConnRegistry.Load(imei); ok {
		if dc, ok := val.(DeviceConn); ok {
			_, err := dc.Conn.Write(data)
			return err == nil
		}
	}
	return false
}

func GetDeviceConn(imei string) (*DeviceConn, bool) {
    if val, ok := ConnRegistry.Load(imei); ok {
        if dc, ok := val.(DeviceConn); ok {
            return &dc, true
        }
    }
    return nil, false
}
