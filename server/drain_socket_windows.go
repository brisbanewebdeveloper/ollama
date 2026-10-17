package server

import "os"

// The private Unix control channel is unavailable on Windows.
func privateDrainDirectory(os.FileInfo) bool { return false }
func staleDrainSocket(error) bool            { return false }
