package gopad

import (
	"bufio"
	"bytes"
	"encoding/gob"
	"encoding/json"
	"log"
	"math/rand"
	"net"
	"net/rpc"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
)

var (
	updateDelay = 250 * time.Millisecond
)

type UserData struct {
	doc *Doc
	xid uint32
}

type Server struct {
	listener     net.Listener
	commitLog    []Op
	doc          *Doc
	px           *Paxos
	mu           sync.Mutex
	userSeqs     map[int]uint32 // last reported sequence by user
	userViews    map[int]uint32 // last reported view number by user
	numusers     int
	commitpoint  uint32 // the upper bound of our commit log (in absolute terms)
	discardpoint uint32 // ops below this have been discarded
	userData     map[int]*UserData
	seq          int
	reboot       bool
	servers      []string
	me           int
	port         int
	// handler  map[string]HandleFunc
	// m sync.RWMutex
}

func (s *Server) Copy(args *RecoverArg, reply *RecoverReply) {
	if !s.reboot {
		s.mu.Lock()
		s.px.Lock()

		var buf1, buf2 bytes.Buffer

		encoder := gob.NewEncoder(&buf1)
		encoder.Encode(s)
		reply.Srv = buf1.String()

		encoder = gob.NewEncoder(&buf2)
		encoder.Encode(s.px)
		reply.Px = buf2.String()

		reply.Err = "OK"

		s.px.Unlock()
		s.mu.Unlock()
	} else {
		reply.Err = "REBOOT"
	}
}

func (s *Server) Recover(servers []string) {
	done := false
	var reply RecoverReply

	for !done {
		for _, srv := range servers {
			ok := call(srv, "NewServer.Copy", &RecoverArg{}, &reply, false)
			if ok && reply.Err == "OK" {
				done = true
				s.reboot = false
			}
		}
	}
}

func NewServer(fname string, reboot bool, port int, servers []string, me int) *Server {
	var doc Doc
	s := Server{
		reboot:  reboot,
		servers: servers,
		me:      me,
		port:    port,
	}

	if !reboot {
		s.userSeqs = make(map[int]uint32)
		s.userViews = make(map[int]uint32)
		s.commitLog = make([]Op, 0)
		s.userData = make(map[int]*UserData)

		// new document, so start fresh
		doc = Doc{
			Id:      rand.Uint32(),
			Seqs:    make(map[int]uint32),
			UserPos: make(map[int]*Pos),
			Colors:  make(map[int]int),
		}

		if fname != "" {
			file, err := os.Open(fname)
			if err != nil {
				log.Fatal(err)
			}
			defer file.Close()
			scanner := bufio.NewScanner(file)
			for scanner.Scan() {
				doc.Rows = append(doc.Rows,
					erow{
						Chars:  scanner.Text(),
						Temp:   make([]bool, len(scanner.Text())),
						Author: make([]int, len(scanner.Text())),
					})
			}
		} else {
			// empty first row
			doc.Rows = append(doc.Rows,
				erow{
					Chars:  "",
					Temp:   make([]bool, 0),
					Author: make([]int, 0),
				})
		}

		s.doc = &doc
	} else {
		s.Recover(servers)
	}

	return &s
}

func (s *Server) getOp(seq int) Paxage {
	to := 10 * time.Millisecond
	for {
		status, val := s.px.status(seq)
		if status == Decided {
			return val.(Paxage)
		}

		time.Sleep(to)
		if to < 10*time.Second {
			to *= 2
		}
	}
}

func (s *Server) handleOp(ops []Op) {
	xid := rand.Int63()
	for {
		s.seq++
		s.px.Start(s.seq, Paxage{ops, xid})
		pkg := s.getOp(s.seq)

		if pkg.Xid == xid {
			break
		}
	}
}

func (s *Server) Init(arg InitArg, reply *InitReply) error {
	log.Println("Sending initial...", arg.Client)

	if len(s.doc.Colors) >= MAXUSERS {
		reply.Err = "Full"
		return nil
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if s.userData[arg.Client] == nil {
		// new user
		s.handleOp([]Op{Op{Type: Init, View: arg.Xid, Client: arg.Client, Seq: 1}})
		reply.Err = "Redo"
	} else if s.userData[arg.Client].xid != arg.Xid {
		// old user new session
		s.handleOp([]Op{Op{Type: Init, View: arg.Xid, Client: arg.Client, Seq: s.userSeqs[arg.Client] + 1}})
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
	}

	return nil
}

// get committed but not discarded ops
func (s *Server) Query(arg QueryArg, reply *QueryReply) error {
	idx := arg.View

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
		log.Println("Couldn't unmarshal op", err)
		reply.Err = "Encode"
		return nil
	}

	// log.Printf("RECEIVED: %v\n", ops)

	s.mu.Lock()
	defer s.mu.Unlock()
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
		s.handleOp(ops)
	}

	reply.Err = "OK"
	return nil
}

// apply log
func (s *Server) update() {
	seq := 1
	var ops []Op
	for {
		status, val := s.px.status(seq)
		if status == Pending {
			time.Sleep(updateDelay)
			continue
		}
		seq++

		// get package from paxos
		ops = (val.(Paxage)).Payload.([]Op)
		s.mu.Lock()

		// append to commit log
		for _, c := range ops {
			if c.Seq == s.userSeqs[c.Client]+1 {
				s.commitLog = append(s.commitLog, c)
				s.commitpoint++
				s.userSeqs[c.Client]++

				s.doc.apply(c, false)
				s.doc.Seqs[c.Client]++

				// TODO: handle Inits
				if c.Type == Init {
					if s.userData[c.Client] == nil || s.userData[c.Client].xid != c.View {
						s.userData[c.Client] = &UserData{doc: s.doc.dup(), xid: c.View}
						s.userData[c.Client].doc.View++
						s.userViews[c.Client] = s.userData[c.Client].doc.View // correct?
					}
				}

				s.doc.View++
			}
			if s.userViews[c.Client] < c.View && c.Type != Init {
				s.userViews[c.Client] = c.View
			}
		}

		s.px.Done(seq)

		var min uint32
		for _, v := range s.userViews {
			if min == 0 {
				min = v
			} else if min > v {
				min = v
			}
		}
		if s.discardpoint < min {
			s.commitLog = s.commitLog[min-s.discardpoint:]
			s.discardpoint = min
		}
		s.mu.Unlock()
		time.Sleep(updateDelay)
	}
}

func (s *Server) Start() {
	rpcs := rpc.NewServer()
	rpcs.Register(s)

	addr := ":" + strconv.Itoa(s.port)

	var err error
	s.listener, err = net.Listen("tcp", addr)
	if err != nil {
		if strings.HasSuffix(err.Error(), ": address already in use") {
		} else {
			log.Fatal(err)
		}
	}

	gob.Register([]Op{})
	gob.Register(Paxage{})

	s.px = makePaxos(s.servers, s.me)
	rpcs.Register(s.px)

	log.Println("Listening on", s.listener.Addr().String())

	go s.update()

	for {
		conn, err := s.listener.Accept()
		if err == nil {
			go rpcs.ServeConn(conn)
		}
	}
}
