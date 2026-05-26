package main

import "time"

// Transport
const (
	pollTimeout = 30
	clientTimeout = pollTimeout + 5
	getMethodLimit = 1
	pollRetryBackoff = 5 * time.Second
)