package main

import (
	"flag"
	"fmt"
	"math/rand"
	// "os"
	"time"
	// "sync"
)

func main() {
	rand.Seed(time.Now().UnixNano())
	user := flag.Int("u", -1, "userid")
	server := flag.String("s", "localhost", "server address")
	port := flag.Int("p", Port, "port")

	flag.Parse()
	args := flag.Args()

	if *user == -1 {
		file := ""
		if len(args) > 0 {
			file = args[0]
		}
		s := NewServer(file)
		s.start()
	} else {
		if *user < 0 {
			fmt.Println("User id is mandatory!  Use -u flag.")
		} else {
			StartClient(*user, *server, *port)
		}
	}
}
