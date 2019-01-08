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

type UserData struct {
	doc *Doc
}

type Server struct {
	listener     net.Listener
	commitLog    []Op
	doc          Doc
	px           Paxos
	mu           sync.Mutex
	userSeqs     map[uint32]uint32 // last sequence number by user
	userViews    map[uint32]int    // last reported view number by user
	numusers     int
	commitpoint  int
	discardpoint int
	userData     map[uint32]*UserData

	// handler  map[string]HandleFunc
	// m sync.RWMutex
}

func NewServer() *Server {
	file, err := os.Open("test")
	if err != nil {
		log.Fatal(err)
	}
	defer file.Close()

	doc := Doc{
		Id:    rand.Uint32(),
		Seqs:  make(map[uint32]uint32),
		Users: make(map[uint32]*Pos),
	}
	e := Server{
		doc:       doc,
		userSeqs:  make(map[uint32]uint32),
		userViews: make(map[uint32]int),
		commitLog: make([]Op, 0),
		userData:  make(map[uint32]*UserData),
	}
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		e.doc.Rows = append(e.doc.Rows,
			erow{
				Chars:  scanner.Text(),
				Temp:   make([]bool, len(scanner.Text())),
				Author: make([]uint32, len(scanner.Text())),
			})
	}

	return &e
}

func (s *Server) handleOp(ops []Op) {
	s.mu.Lock()
	for _, c := range ops {
		s.commitLog = append(s.commitLog, c)
		s.commitpoint++
		if c.Seq == s.userSeqs[c.Client]+1 {
			s.userSeqs[c.Client]++
		}
		if s.userViews[c.Client] < c.View {
			s.userViews[c.Client] = c.View
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

	if s.userData[arg.Client] == nil {
		go s.handleOp([]Op{Op{Type: Init, Client: arg.Client, Seq: 1}})
		reply.Err = "Redo"
	} else {
		// marshal document and send back
		buf, err := docToBytes(s.userData[arg.Client].doc)
		if err != nil {
			log.Println("Couldn't send document", err)
			reply.Err = "Encode"
			return nil
		}
		reply.Doc = buf
		reply.Err = "OK"

		s.numusers++
	}

	return nil
}

func (s *Server) Query(arg QueryArg, reply *QueryReply) error {
	idx := arg.View

	// log.Println(idx, "----", s.discardpoint, s.commitpoint)
	// log.Println("Sending query...", s.view-idx)
	if idx > s.commitpoint {
		reply.Err = "BAD"
		return nil
	}

	s.mu.Lock()
	if s.userViews[arg.Client] < arg.View {
		s.userViews[arg.Client] = arg.View
	}
	buf, err := json.Marshal(s.commitLog[idx-s.discardpoint : s.commitpoint-s.discardpoint])
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
		if ops[0].Seq > s.doc.Seqs[ops[0].Client]+1 {
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

		if ops[len(ops)-1].Seq > s.doc.Seqs[ops[0].Client] {
			// there is a new op
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

		if s.doc.View < s.commitpoint {
			for _, c := range s.commitLog[s.doc.View-s.discardpoint : s.commitpoint-s.discardpoint] {
				if c.Seq == s.doc.Seqs[c.Client]+1 {
					s.doc.apply(c, false)
					s.doc.Seqs[c.Client]++

					// TODO: handle Inits
					if c.Type == Init && s.userData[c.Client] == nil {
						s.userData[c.Client] = &UserData{doc: s.doc.copy()}
						s.userData[c.Client].doc.View++
						s.userViews[c.Client] = s.userData[c.Client].doc.View // correct?
					}

					if c.Type == Save {
						f, err := os.Create("tmp1")
						if err != nil {
							log.Fatal(err)
						}

						for _, r := range s.doc.Rows {
							f.WriteString(r.Chars + string('\n'))
						}
						f.Sync()
						f.Close()
						os.Rename("tmp1", "tmp")
					}
				}
				s.doc.View++
			}

		}

		min := 0
		for _, v := range s.userViews {
			if min == 0 {
				min = v
			} else if min > v {
				min = v
			}
		}
		if s.discardpoint < min {
			s.commitLog = s.commitLog[min-s.discardpoint:]
			log.Printf("Chopped %d from log.  New length: %d\n", min-s.discardpoint, len(s.commitLog))
			s.discardpoint = min
		}
		s.mu.Unlock()
		time.Sleep(250 * time.Millisecond)
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
