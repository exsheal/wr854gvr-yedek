package main

import "time"

// modTime is fixed so repeated runs produce byte-identical tarballs,
// which makes the on-device self-checksum predictable and diffable.
var modTime = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
