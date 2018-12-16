package main

import (
	"bufio"
	"bytes"
	"encoding/gob"
	"io"
	"log"
	"net"
	"net/http"
	"net/rpc"
	"os"
	"strings"
	// "sync"
)

type Server struct {
	listener net.Listener
	doc      Doc

	// handler  map[string]HandleFunc
	// m sync.RWMutex
}

func NewServer() *Server {
	file, err := os.Open("test")
	if err != nil {
		log.Fatal(err)
	}
	defer file.Close()

	e := Server{doc: Doc{}}
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		e.doc.Rows = append(e.doc.Rows, erow{Chars: scanner.Text()})
	}

	return &e
}

func (e *Server) handle(conn net.Conn) {
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

func (s *Server) Commit(arg *Args, reply *Reply) error {
	log.Println("Got rune", string(arg.Data))
	reply.Err = "OK"
	return nil
}

func (s *Server) Init(arg *Args, reply *Reply) error {
	log.Println("Sending initial...")

	var buf bytes.Buffer
	enc := gob.NewEncoder(&buf)
	err := enc.Encode(s.doc)
	reply.Data = buf.Bytes()

	if err != nil {
		log.Println("Couldn't send document", err)
		reply.Err = "Encode"
		return nil
	}
	return nil
}

func (s *Server) start() {
	err := rpc.Register(s)
	if err != nil {
		log.Fatal("Couldn't register RPC", err)
	}
	rpc.HandleHTTP()

	s.listener, err = net.Listen("tcp", Port)
	if err != nil {
		log.Fatal("Couldn't listen")
	}

	log.Println("Listening on", s.listener.Addr().String())

	http.Serve(s.listener, nil)

	// for {
	// 	log.Println("Accepting requests")
	// 	conn, err := e.listener.Accept()
	// 	if err != nil {
	// 		log.Println("Failed to accept")
	// 		continue
	// 	}
	// 	log.Println("Got a connection")
	// 	go e.handle(conn)
	// }
}
