package main

import "errors"

// errNATS is reported by the /healthz NATS probe when the client is disconnected
// (the connection reconnects in the background, so this is a readiness signal).
var errNATS = errors.New("nats: disconnected")
