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
	listener net.Listener
	commits  []Commit
	doc      Doc
	view     int
	px       Paxos
	mu       sync.Mutex

	// handler  map[string]HandleFunc
	// m sync.RWMutex
}

func NewServer() *Server {
	file, err := os.Open("test")
	if err != nil {
		log.Fatal(err)
	}
	defer file.Close()

	e := Server{doc: Doc{}, commits: make([]Commit, 0)}
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		e.doc.Rows = append(e.doc.Rows, erow{Chars: scanner.Text()})
	}

	return &e
}

// func (s *Server) handle(conn net.Conn) {
// 	defer conn.Close()

// 	// send initial document
// 	enc := gob.NewEncoder(conn)
// 	err := enc.Encode(s.doc)
// 	if err != nil {
// 		conn.Close()
// 		log.Println("Couldn't send document", err)
// 		return
// 	}
// 	rw := bufio.NewReadWriter(bufio.NewReader(conn), bufio.NewWriter(conn))

// 	for {
// 		// wait for command
// 		log.Print("Waiting to receive")
// 		cmd, err := rw.ReadString('\n')
// 		switch {
// 		case err == io.EOF:
// 			log.Println("Reached EOF")
// 			return
// 		case err != nil:
// 			log.Println("Error reading")
// 			return
// 		}

// 		cmd = strings.Trim(cmd, "\n ")
// 		log.Println("Received:", cmd)
// 		rw.WriteString(cmd)
// 		rw.Flush()
// 	}
// }

func (s *Server) handleCommit(commits []Commit) {
	log.Println("Got", len(commits), "runes")
	for _, c := range commits {
		log.Println(string(c.Data))
	}
}

func (s *Server) Handle(arg Arg, reply *Reply) error {
	switch arg.Op {
	case "Commit":
		// unmarshal commit array
		var commits []Commit
		err := json.Unmarshal(arg.Data, &commits)
		if err != nil {
			log.Println("Couldn't unmarshal commits", err)
			reply.Err = "Encode"
			return nil
		}
		go s.handleCommit(commits)

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
	case "Query":
		log.Println("Sending query...")

		idx, num := binary.Varint(arg.Data)
		if num == 0 {
			log.Println("Couldn't decode query")
			reply.Err = "Decode"
			return nil
		}

		s.mu.Lock()
		buf, err := json.Marshal(s.commits[idx:s.view])
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
