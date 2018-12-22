package main

import (
	"bufio"
	// "encoding/binary"
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
	view      uint32
	px        Paxos
	mu        sync.Mutex
	users     map[int]int

	// handler  map[string]HandleFunc
	// m sync.RWMutex
}

func NewServer() *Server {
	file, err := os.Open("test")
	if err != nil {
		log.Fatal(err)
	}
	defer file.Close()

	e := Server{doc: Doc{}, users: make(map[int]int), commitLog: make([]Op, 0)}
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
		s.view++
	}
	s.mu.Unlock()
}

func (s *Server) Init(arg Arg, reply *InitReply) error {

	log.Println("Sending initial...")

	s.commitLog = append(s.commitLog, Op{Op: Init, Client: byteToInt(arg.Data)})
	s.view++

	// marshal document and send back
	buf, err := json.Marshal(s.doc)
	if err != nil {
		log.Println("Couldn't send document", err)
		reply.Err = "Encode"
		return nil
	}
	reply.Doc = buf

	buf, err = json.Marshal(s.commitLog)
	if err != nil {
		log.Println("Couldn't send log", err)
		reply.Err = "Encode"
		return nil
	}
	reply.Commits = buf
	reply.Err = "OK"

	return nil
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

	case "Query":

		idx := byteToInt(arg.Data)

		log.Println("Sending query...", s.view-idx)

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
