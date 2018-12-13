package main

import (
	"bufio"
	"encoding/gob"
	"io"
	"log"
	"net"
	"os"
	"strings"
	// "sync"
)

type server struct {
	listener net.Listener
	doc      Doc

	// handler  map[string]HandleFunc
	// m sync.RWMutex
}

/*** row operations ***/

func NewServer() *server {
	file, err := os.Open("test")
	if err != nil {
		log.Fatal(err)
	}
	defer file.Close()

	e := server{doc: Doc{}}
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		e.doc.Rows = append(e.doc.Rows, erow{Chars: scanner.Text()})
	}

	return &e
}

func (e *server) handle(conn net.Conn) {
	defer conn.Close()

	// send initial document
	enc := gob.NewEncoder(conn)
	err := enc.Encode(e.doc)
	if err != nil {
		conn.Close()
		log.Println("Couldn't send document", err)
		return
	}
	rw := bufio.NewReadWriter(bufio.NewReader(conn), bufio.NewWriter(conn))

	for {
		// wait for command
		log.Print("Waiting to receive")
		cmd, err := rw.ReadString('\n')
		switch {
		case err == io.EOF:
			log.Println("Reached EOF")
			return
		case err != nil:
			log.Println("Error reading")
			return
		}

		cmd = strings.Trim(cmd, "\n ")
		log.Println("Received:", cmd)
		rw.WriteString(cmd)
		rw.Flush()
	}
}

func (e *server) Listen() error {
	var err error
	e.listener, err = net.Listen("tcp", Port)
	if err != nil {
		log.Fatal("Couldn't listen")
	}

	log.Println("Listening on", e.listener.Addr().String())

	for {
		log.Println("Accepting requests")
		conn, err := e.listener.Accept()
		if err != nil {
			log.Println("Failed to accept")
			continue
		}
		log.Println("Got a connection")
		go e.handle(conn)
	}
}
