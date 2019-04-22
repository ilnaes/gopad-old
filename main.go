package main

import (
	"flag"
	"fmt"
	"math/rand"
	// "os"
	"github.com/ilnaes/gopad/src"
	"time"
	// "sync"
)

func main() {
	rand.Seed(time.Now().UnixNano())
	user := flag.Int("u", -1, "userid")
	server := flag.String("s", "localhost", "server address")
	port := flag.Int("p", gopad.Port, "port")

	flag.Parse()
	args := flag.Args()

	if *user == -1 {
		file := ""
		if len(args) > 0 {
			file = args[0]
		}
		s := gopad.NewServer(file)
		s.Start()
	} else {
		if *user < 0 {
			fmt.Println("User id is mandatory!  Use -u flag.")
		} else {
			gopad.StartClient(*user, *server, *port, false)
		}
	}
}
