package main

import (
	"flag"
	"fmt"
	"math/rand"
	// "os"
	"github.com/ilnaes/gopad-old/src"
	"time"
	// "sync"
)

func main() {
	rand.Seed(time.Now().UnixNano())
	user := flag.Int("u", -1, "userid")
	server := flag.String("s", "localhost", "server address")
	me := flag.Int("m", -1, "me")
	port := flag.Int("p", gopad.Port, "port")
	reboot := flag.Bool("r", false, "a bool")

	flag.Parse()
	args := flag.Args()

	// var b chan (int)
	host := "localhost"

	if *user == -1 {
		file := ""
		if len(args) > 0 {
			file = args[0]
		}

		servers := []string{host + ":6060", host + ":6061", host + ":6062"}

		s1 := gopad.NewServer(file, *reboot, *port, servers, *me)
		s1.Start()
		// s2 := gopad.NewServer(file, *reboot, 6061, servers, 1)
		// go s2.Start()
		// s3 := gopad.NewServer(file, *reboot, 6062, servers, 2)
		// go s3.Start()

		// x := <-b
		// fmt.Println(x)

	} else {
		if *user < 0 {
			fmt.Println("User id is mandatory!  Use -u flag.")
		} else {
			gopad.StartClient(*user, *server, *port, false)
		}
	}
}
