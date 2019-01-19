package main

import (
	"flag"
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
		user := flag.Int("u", 1, "userid")
		server := flag.String("s", "localhost", "server address")
		flag.Parse()
		StartClient(*user, *server)
	}
}
