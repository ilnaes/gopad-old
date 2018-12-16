package main

import (
	"os"
	// "sync"
)

func main() {
	if len(os.Args) == 1 {
		s := NewServer()
		s.start()
	} else {
		StartClient(os.Args[1])
	}
}
