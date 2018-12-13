package main

import (
	"os"
	// "sync"
)

func main() {
	if len(os.Args) == 1 {
		s := NewServer()
		s.Listen()
	} else {
		StartClient(os.Args[1])
	}
}
