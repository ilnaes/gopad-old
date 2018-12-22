package main

import (
	"math/rand"
	"os"
	"time"
	// "sync"
)

func main() {
	rand.Seed(time.Now().UnixNano())
	if len(os.Args) == 1 {
		s := NewServer()
		s.start()
	} else {
		StartClient(os.Args[1])
	}
}
