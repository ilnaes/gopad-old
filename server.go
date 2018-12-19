package main

import (
	"bufio"
	"encoding/binary"
	"encoding/json"
	"log"
	"net"
	"net/rpc"
	"os"
	"sync"
)

type Server struct {
	listener  net.Listener
	commitLog []Op
	doc       Doc
	view      int
	px        Paxos
	mu        sync.Mutex

	// handler  map[string]HandleFunc
	// m sync.RWMutex
}

func NewServer() *Server {
	file, err := os.Open("test")
	if err != nil {
		log.Fatal(err)
	}
	defer file.Close()

	e := Server{doc: Doc{}, commitLog: make([]Op, 0)}
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		e.doc.Rows = append(e.doc.Rows, erow{Chars: scanner.Text(), Temp: make([]bool, len(scanner.Text()))})
	}

	return &e
}

func (s *Server) handleOp(ops []Op) {
	log.Println("Got", len(ops), "ops")
	s.mu.Lock()
	for _, c := range ops {
		log.Println(c)
		s.commitLog = append(s.commitLog, c)
	}
	s.mu.Unlock()
}

func (s *Server) Handle(arg Arg, reply *Reply) error {
	switch arg.Op {
	case "Op":
		// unmarshal commit array
		var ops []Op
		err := json.Unmarshal(arg.Data, &ops)
		if err != nil {
			log.Println("Couldn't unmarshal ops", err)
			reply.Err = "Encode"
			return nil
		}
		go s.handleOp(ops)

		reply.Err = "OK"

	case "Init":
		log.Println("Sending initial...")

		// marshal document and send back
		buf, err := json.Marshal(s.doc)
		if err != nil {
			log.Println("Couldn't send document", err)
			reply.Err = "Encode"
			return nil
		}
		reply.Data = buf
		reply.Err = "OK"

	case "Query":
		log.Println("Sending query...")

		idx, num := binary.Varint(arg.Data)
		if num == 0 {
			log.Println("Couldn't decode query")
			reply.Err = "Decode"
			return nil
		}

		s.mu.Lock()
		buf, err := json.Marshal(s.commitLog[idx:s.view])
		s.mu.Unlock()

		if err != nil {
			log.Println("Couldn't send document", err)
			reply.Err = "Encode"
			return nil
		}
		reply.Data = buf
	}
	reply.Err = "OK"
	return nil
}

// apply log
func (s *Server) update() {
}

func (s *Server) start() {
	rpcs := rpc.NewServer()
	rpcs.Register(s)

	var err error
	s.listener, err = net.Listen("tcp", Port)
	if err != nil {
		log.Fatal("Couldn't listen")
	}

	log.Println("Listening on", s.listener.Addr().String())

	go s.update()

	for {
		conn, err := s.listener.Accept()
		if err == nil {
			go rpcs.ServeConn(conn)
		}
	}
}
