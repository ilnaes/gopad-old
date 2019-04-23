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

	var b chan (int)
	host := "localhost"

	if *user == -1 {
		file := ""
		if len(args) > 0 {
			file = args[0]
		}

		servers := []string{host + ":6060", host + ":6061", host + ":6062"}

		s1 := gopad.NewServer(file)
		go s1.Start(6060, 0, servers)
		s2 := gopad.NewServer(file)
		go s2.Start(6061, 1, servers)
		s3 := gopad.NewServer(file)
		go s3.Start(6062, 2, servers)

		x := <-b
		fmt.Println(x)

	} else {
		if *user < 0 {
			fmt.Println("User id is mandatory!  Use -u flag.")
		} else {
			gopad.StartClient(*user, *server, *port, false)
		}
	}
}
