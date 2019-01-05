package main

import (
	"bufio"
	// "encoding/binary"
	"encoding/json"
	"log"
	"math/rand"
	"net"
	"net/rpc"
	"os"
	"sync"
	"time"
)

type Server struct {
	listener  net.Listener
	commitLog []Op
	doc       Doc
	view      uint32
	px        Paxos
	mu        sync.Mutex
	userSeqs  map[uint32]uint32 // last sequence number by user
	userViews map[uint32]uint32 // last reported view number by user
	numusers  int

	// handler  map[string]HandleFunc
	// m sync.RWMutex
}

func NewServer() *Server {
	file, err := os.Open("test")
	if err != nil {
		log.Fatal(err)
	}
	defer file.Close()

	e := Server{doc: Doc{Id: rand.Uint32()}, userSeqs: make(map[uint32]uint32), userViews: make(map[uint32]uint32), commitLog: make([]Op, 0)}
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		e.doc.Rows = append(e.doc.Rows,
			erow{Chars: scanner.Text(), Temp: make([]bool, len(scanner.Text())), Author: make([]uint32, len(scanner.Text()))})
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
		if c.Seq == s.userSeqs[c.Client]+1 {
			s.userSeqs[c.Client]++
		}
	}
	s.mu.Unlock()
}

func (s *Server) Init(arg InitArg, reply *InitReply) error {
	log.Println("Sending initial...")

	if s.numusers >= MAXUSERS {
		reply.Err = "Full"
		return nil
	}

	s.commitLog = append(s.commitLog, Op{Type: Init, Client: arg.Client, Seq: 1})
	s.view++

	s.userSeqs[arg.Client] = 1

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

	s.numusers++

	return nil
}

func (s *Server) Query(arg QueryArg, reply *QueryReply) error {
	idx := arg.View

	// log.Println("Sending query...", s.view-idx)

	s.mu.Lock()
	buf, err := json.Marshal(s.commitLog[idx:s.view])
	s.mu.Unlock()

	if err != nil {
		log.Println("Couldn't send document", err)
		reply.Err = "Encode"
		return nil
	}
	reply.Data = buf
	reply.Err = "OK"
	return nil
}

func (s *Server) Handle(arg OpArg, reply *OpReply) error {
	// unmarshal commit array
	var ops []Op
	err := json.Unmarshal(arg.Data, &ops)
	if err != nil {
		log.Println("Couldn't unmarshal ops", err)
		reply.Err = "Encode"
		return nil
	}

	if len(ops) > 0 {
		if ops[0].Seq > s.userSeqs[ops[0].Client]+1 {
			// sequence number larger than expected
			reply.Err = "High"
			return nil
		}

		if len(ops) > 1 {
			for i := 1; i < len(ops); i++ {
				if ops[i].Seq != ops[i-1].Seq+1 {
					// out of sequential order
					reply.Err = "Order"
					return nil
				}

				if ops[i].Client != ops[i-1].Client {
					// different client ids
					reply.Err = "Client"
					return nil
				}
			}
		}

		if ops[len(ops)-1].Seq > s.userSeqs[ops[0].Client] {
			go s.handleOp(ops)
		}
	}

	reply.Err = "OK"
	return nil
}

// apply log
func (s *Server) update() {
	for true {
		s.mu.Lock()

		s.mu.Unlock()
		time.Sleep(time.Second)
	}
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
