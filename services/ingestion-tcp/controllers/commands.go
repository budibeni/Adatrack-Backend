package controllers

// commands.go — B8 downlink ("server → device") command encoding.
//
// The reference for the GT06 online command is the vendor protocol
// (docs/docs-device/GPS_Tracker_communication_protocol_v3.1.md §8.1):
//
//	0x78 0x78 | Length | 0x80 | LenOfCommand | ServerFlag(4) | Command(ASCII) |
//	InformationSerial(2) | ErrorCheck(2) | 0x0D 0x0A
//
// The vendor example (`78 78 0E 80 08 ... 00 01 6D 6A 0D 0A`) implies Length
// counts the 2 ErrorCheck bytes, while the client→server framing implemented and
// verified in gt06frame.go (`WriteAck`/`ReadPacket`, round-trip tested) does NOT.
// The two cannot both be right for one firmware, so the server→device frame has
// an explicit toggle (GT06_COMMAND_LENGTH_INCLUDES_CRC, default false = the same
// convention as the verified client→server framing). The device's own reply is
// what settles it, and the raw reply is stored in `td_device_commands.detail`.

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"

	"adatrack_gps/ingestion-tcp/models"
)

// ErrCommandUnsupported is returned for a protocol that has no documented
// downlink command yet. It is explicit instead of best-effort: inventing a frame
// for an undocumented device family would be worse than reporting "unsupported".
var ErrCommandUnsupported = errors.New("downlink command not supported for this protocol")

// CommandEncoder is implemented by decoders that accept server→device commands.
// It is an OPTIONAL interface (type assertion), so a decoder that only ingests
// telemetry does not need to stub it out.
type CommandEncoder interface {
	EncodeCommand(cmd models.DeviceCommand) ([]byte, error)
}

// gt06CommandLengthIncludesCRC toggles the Length-field convention of the
// server→device online command (see the file header note).
var gt06CommandLengthIncludesCRC = envBoolLocal("GT06_COMMAND_LENGTH_INCLUDES_CRC", false)

// SetGT06CommandLengthIncludesCRC overrides the toggle (tests/diagnostics).
func SetGT06CommandLengthIncludesCRC(v bool) { gt06CommandLengthIncludesCRC = v }

// GT06CommandLengthIncludesCRC reports the active convention.
func GT06CommandLengthIncludesCRC() bool { return gt06CommandLengthIncludesCRC }

// envBoolLocal reads a bool env var with a default (kept local so the mapping of
// this one framing quirk stays next to the code that implements it).
func envBoolLocal(key string, def bool) bool {
	v, ok := os.LookupEnv(key)
	if !ok || strings.TrimSpace(v) == "" {
		return def
	}
	b, err := strconv.ParseBool(strings.TrimSpace(v))
	if err != nil {
		return def
	}
	return b
}

// EncodeCommand implements CommandEncoder for the TK103 family. The command
// letters are the upstream Tk103ProtocolEncoder ones (standard, non-"sms2"
// variant), wrapped in the `(...)` frame the family expects:
//
//	(<IMEI>AV010)            engine stop / cut oil-electricity
//	(<IMEI>AV011)            engine resume
//	(<IMEI>AT00)             reboot
//	(<IMEI>AP00)             single position request
//	(<IMEI>AR00<FREQ4HEX>0000) periodic position (FREQ = seconds, 4 uppercase hex)
func (tk103Decoder) EncodeCommand(cmd models.DeviceCommand) ([]byte, error) {
	id := cmd.IMEI
	if id == "" {
		return nil, &models.CommandError{Field: "imei", Msg: "imei is required for a TK103 command"}
	}
	switch cmd.Kind {
	case models.CommandEngineCut:
		return []byte("(" + id + "AV010)"), nil
	case models.CommandEngineRestore:
		return []byte("(" + id + "AV011)"), nil
	case models.CommandReboot:
		return []byte("(" + id + "AT00)"), nil
	case models.CommandLocate:
		return []byte("(" + id + "AP00)"), nil
	case models.CommandSetInterval:
		if cmd.IntervalSeconds < 5 || cmd.IntervalSeconds > 86400 {
			return nil, &models.CommandError{Field: "interval_seconds",
				Msg: "interval_seconds must be between 5 and 86400"}
		}
		return []byte(fmt.Sprintf("(%sAR00%04X0000)", id, cmd.IntervalSeconds)), nil
	default:
		return nil, fmt.Errorf("%w: %s (tk103)", ErrCommandUnsupported, cmd.Kind)
	}
}

// EncodeCommand implements CommandEncoder for GT06/Concox.
func (gt06Decoder) EncodeCommand(cmd models.DeviceCommand) ([]byte, error) {
	content, err := GT06CommandContent(cmd)
	if err != nil {
		return nil, err
	}
	return BuildGT06OnlineCommand(content, nextCommandSerial()), nil
}

// GT06CommandContent maps a whitelisted command to the ASCII content the device
// understands (the same strings its SMS interface accepts — v3.1 §8.1).
func GT06CommandContent(cmd models.DeviceCommand) (string, error) {
	switch cmd.Kind {
	case models.CommandEngineCut:
		return "DYD#", nil
	case models.CommandEngineRestore:
		return "HFYD#", nil
	case models.CommandSetInterval:
		if cmd.IntervalSeconds < 5 || cmd.IntervalSeconds > 86400 {
			return "", &models.CommandError{Field: "interval_seconds",
				Msg: "interval_seconds must be between 5 and 86400"}
		}
		return fmt.Sprintf("TIMER,%d#", cmd.IntervalSeconds), nil
	case models.CommandReboot:
		return "RESET#", nil
	case models.CommandLocate:
		return "DWXX#", nil
	default:
		return "", fmt.Errorf("%w: %s", ErrCommandUnsupported, cmd.Kind)
	}
}

// BuildGT06OnlineCommand frames one 0x80 online command.
func BuildGT06OnlineCommand(content string, serial uint16) []byte {
	cmdLen := 4 + len(content) // server flag (4) + command content
	inner := []byte{0x00, models.ProtoOnlineCommand, byte(cmdLen)}
	inner = append(inner, 0x00, 0x00, 0x00, 0x00) // server flag (echoed in the reply)
	inner = append(inner, []byte(content)...)
	inner = append(inner, byte(serial>>8), byte(serial))

	length := 1 + 1 + cmdLen + 2 // proto + len-of-command + command + serial
	if gt06CommandLengthIncludesCRC {
		length += 2
	}
	inner[0] = byte(length)

	sum := crc16(inner)
	frame := make([]byte, 0, len(inner)+6)
	frame = append(frame, models.FrameStartShort, models.FrameStartShort)
	frame = append(frame, inner...)
	frame = append(frame, byte(sum>>8), byte(sum), models.FrameStop0, models.FrameStop1)
	return frame
}

// commandSerial is a process-wide monotonic Information Serial Number (the
// vendor spec: "serial number of data sent later at each time will be
// automatically added 1").
var commandSerialCounter uint32

// nextCommandSerial returns the next serial number (wraps at 0xFFFF, skipping 0).
func nextCommandSerial() uint16 {
	commandSerialCounter++
	if commandSerialCounter > 0xFFFF {
		commandSerialCounter = 1
	}
	return uint16(commandSerialCounter)
}

// parseGT06CommandReply turns an online-command reply content into the result
// detail recorded in `td_device_commands`. The GT06 terminal answers `0x21` with
// the command content it executed, e.g. `DYD=Success!` / `HFYD=Fail!`.
func parseGT06CommandReply(content string) (ok bool, detail string) {
	c := strings.TrimSpace(content)
	if c == "" {
		return false, "empty device reply"
	}
	upper := strings.ToUpper(c)
	switch {
	case strings.Contains(upper, "SUCCESS"):
		return true, c
	case strings.Contains(upper, "FAIL"), strings.Contains(upper, "ERROR"),
		strings.Contains(upper, "UNVALUED"), strings.Contains(upper, "SPEED LIMIT"):
		return false, c
	default:
		// Unknown/echo-only replies still prove the device answered: record the
		// raw content so the operator can judge (honest, no interpretation).
		return true, c
	}
}

// extractCommandReply pulls the human-readable ASCII content out of an online
// command reply payload (0x21 general reply, 0x15 JM01 reply). Both wrap the
// content in binary fields (server flag, language, serial), so the longest
// printable run is returned instead of a fixed offset — a wrong offset would
// silently truncate the device's answer.
func extractCommandReply(data []byte) string {
	var best, cur []byte
	flush := func() {
		if len(cur) > len(best) {
			best = cur
		}
		cur = nil
	}
	for _, b := range data {
		if b >= 0x20 && b <= 0x7e {
			cur = append(cur, b)
			continue
		}
		flush()
	}
	flush()
	return strings.TrimSpace(string(best))
}
