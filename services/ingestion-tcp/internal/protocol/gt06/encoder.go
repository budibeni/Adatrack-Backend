package gt06

import ()

// Helper function to encode commands
func encodeCommand0x80(cmdStr string) []byte {
	// Length of command string
	cmdBytes := []byte(cmdStr)
	cmdLen := byte(len(cmdBytes))

	// Server flag: 4 bytes, usually 00 00 00 00
	serverFlag := []byte{0x00, 0x00, 0x00, 0x00}

	// Info content = length (1 byte) + server flag (4) + cmdBytes
	infoContent := append([]byte{cmdLen}, serverFlag...)
	infoContent = append(infoContent, cmdBytes...)

	// Info length = info content length + Protocol Number (1) + Info Serial Number (2) + Error Check (2)
	// Actually packet length = info length
	packetLen := byte(len(infoContent) + 5)

	packet := []byte{0x78, 0x78, packetLen, 0x80}
	packet = append(packet, infoContent...)

	// Serial number
	packet = append(packet, 0x00, 0x01)

	// Checksum (ITU V.41)
	crc := calculateCRC(packet[2 : len(packet)-2])
	packet = append(packet, byte(crc>>8), byte(crc&0xFF))

	// Stop bits
	packet = append(packet, 0x0D, 0x0A)
	return packet
}

func calculateCRC(data []byte) uint16 {
	var crc uint16 = 0xFFFF
	for _, b := range data {
		crc ^= uint16(b) << 8
		for i := 0; i < 8; i++ {
			if (crc & 0x8000) != 0 {
				crc = (crc << 1) ^ 0x1021
			} else {
				crc = crc << 1
			}
		}
	}
	return crc ^ 0xFFFF // According to GT06, some don't XOR 0xFFFF, let's just use standard
}
