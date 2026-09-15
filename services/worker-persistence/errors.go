package main

import "errors"

// errNATS is reported by the /healthz NATS probe when the client is disconnected.
var errNATS = errors.New("nats: disconnected")
