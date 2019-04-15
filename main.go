package main

import (
	"flag"
	"fmt"
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
		user := flag.Int("u", -1, "userid")
		server := flag.String("s", "localhost", "server address")
		flag.Parse()
		if *user < 0 {
			fmt.Println("User id is mandatory!  Use -u flag.")
		} else {
			StartClient(*user, *server)
		}
	}
}
