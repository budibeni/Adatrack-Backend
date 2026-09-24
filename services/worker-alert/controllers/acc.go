package controllers

// AccOn reports whether the DEVICE confirmed ACC ON (B6).
//
// The ingestion/telemetry ACC flag is tri-state: `nil` means the frame carried
// no ACC information at all. Treating that as "off" keeps the strict fuel gate
// fail-safe (a drop is never accepted as "engine running" without evidence),
// while a literal `false` is a real device reading.
func AccOn(acc *bool) bool { return acc != nil && *acc }

// BoolPtr is the test-friendly constructor of the tri-state flag (B6).
func BoolPtr(v bool) *bool { return &v }
