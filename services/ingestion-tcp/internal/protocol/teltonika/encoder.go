package teltonika

import (
	"encoding/binary"
	"github.com/sigurn/crc16"
)

func encodeCodec12(cmdStr string) []byte {
	cmdBytes := []byte(cmdStr)
	cmdSize := uint32(len(cmdBytes))

	// Data part
	data := []byte{0x0C, 0x01, 0x05} // Codec 12, Qty 1, Type 5 (ASCII)

	cmdSizeBytes := make([]byte, 4)
	binary.BigEndian.PutUint32(cmdSizeBytes, cmdSize)
	data = append(data, cmdSizeBytes...)
	data = append(data, cmdBytes...)
	data = append(data, 0x01) // Qty 2

	// Calculate CRC-16/ARC over data
	table := crc16.MakeTable(crc16.CRC16_ARC)
	crc := crc16.Checksum(data, table)

	// Final packet
	packet := []byte{0x00, 0x00, 0x00, 0x00} // Preamble

	dataSize := uint32(len(data))
	dataSizeBytes := make([]byte, 4)
	binary.BigEndian.PutUint32(dataSizeBytes, dataSize)
	packet = append(packet, dataSizeBytes...)

	packet = append(packet, data...)

	crcBytes := make([]byte, 4)
	binary.BigEndian.PutUint32(crcBytes, uint32(crc))
	packet = append(packet, crcBytes...)

	return packet
}
