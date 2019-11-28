package gopad

import (
	"bufio"
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
	Doc *Doc
	Xid uint32
}

type Server struct {
	listener net.Listener
	px       *Paxos
	mu       sync.Mutex

	// config
	reboot  bool
	servers []string
	me      int
	port    int

	// data
	Doc          Doc
	CommitLog    []Op
	UserSeqs     map[int]uint32 // last reported sequence by user
	UserViews    map[int]uint32 // last reported view number by user
	CommitPoint  uint32         // the upper bound of our commit log (in absolute terms)
	DiscardPoint uint32         // ops below this have been discarded
	UserSession  map[int]uint32 // xid of current user session
	Seq          int

	// handler  map[string]HandleFunc
	// m sync.RWMutex
}

func (s *Server) Copy(args *RecoverArg, reply *RecoverReply) error {
	if !s.reboot {
		s.mu.Lock()
		s.px.Lock()

		reply.Srv, _ = json.Marshal(s)
		reply.Px, _ = json.Marshal(s.px)

		reply.Err = "OK"

		s.px.Unlock()
		s.mu.Unlock()
		log.Println("HERE")
	} else {
		reply.Err = "REBOOTING"
	}

	return nil
}

func (s *Server) Recover(servers []string) {
	done := false
	var reply RecoverReply
	var tmp Server

	for !done {
		for i, srv := range servers {
			log.Println(i, srv, s.me)
			if i != s.me {
				ok := call(srv, "Server.Copy", RecoverArg{}, &reply, false)
				log.Println(reply.Err)
				if ok && reply.Err == "OK" {
					json.Unmarshal(reply.Srv, &tmp)
					s.px.Recover(reply.Px)
					done = true
				}
			}
			time.Sleep(updateDelay)
		}
	}

	s.Seq = tmp.Seq
}

func NewServer(fname string, reboot bool, port int, servers []string, me int) *Server {
	s := Server{
		reboot:  reboot,
		servers: servers,
		me:      me,
		port:    port,
		px:      makePaxos(servers, me),
	}

	if !reboot {
		s.UserSeqs = make(map[int]uint32)
		s.UserViews = make(map[int]uint32)
		s.CommitLog = make([]Op, 0)
		s.UserSession = make(map[int]uint32)

		// new document, so start fresh
		s.Doc = Doc{
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
				s.Doc.Rows = append(s.Doc.Rows,
					erow{
						Chars:  scanner.Text(),
						Temp:   make([]bool, len(scanner.Text())),
						Author: make([]int, len(scanner.Text())),
					})
			}
		} else {
			// empty first row
			s.Doc.Rows = append(s.Doc.Rows,
				erow{
					Chars:  "",
					Temp:   make([]bool, 0),
					Author: make([]int, 0),
				})
		}
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
		s.Seq++
		s.px.Start(s.Seq, Paxage{ops, xid})
		pkg := s.getOp(s.Seq)

		if pkg.Xid == xid {
			break
		}
	}
}

func (s *Server) Init(arg InitArg, reply *InitReply) error {
	log.Println("Sending initial...", arg.Client)

	s.mu.Lock()
	defer s.mu.Unlock()

	if xid, ok := s.UserSession[arg.Client]; !ok {

		// new user
		if len(s.Doc.Colors) >= MAXUSERS {
			reply.Err = "Full"
			return nil
		}

		s.handleOp([]Op{Op{Type: Init, View: arg.Xid, Client: arg.Client, Seq: 1}})
	} else if xid != arg.Xid {
		// old user new session
		s.handleOp([]Op{Op{Type: Init, View: arg.Xid, Client: arg.Client, Seq: s.UserSeqs[arg.Client] + 1}})
	}

	// marshal document and send back
	buf, err := docToBytes(&s.Doc)
	if err != nil {
		log.Println("Couldn't send document", err)
		reply.Err = "Encode"
		return nil
	}
	reply.Doc = buf
	reply.Err = "OK"

	return nil
}

// get committed but not discarded ops
func (s *Server) Query(arg QueryArg, reply *QueryReply) error {
	idx := arg.View

	// log.Println("Sending query...", s.view-idx)
	if idx > s.CommitPoint {
		reply.Err = "BAD"
		return nil
	}

	s.mu.Lock()
	if s.UserViews[arg.Client] < arg.View {
		s.UserViews[arg.Client] = arg.View
	}
	buf, err := json.Marshal(s.CommitLog[idx-s.DiscardPoint : s.CommitPoint-s.DiscardPoint])
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
	if ops[0].Seq > s.Doc.Seqs[ops[0].Client]+1 {
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

	if ops[len(ops)-1].Seq > s.Doc.Seqs[ops[0].Client] {
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
			if c.Seq == s.UserSeqs[c.Client]+1 {
				s.CommitLog = append(s.CommitLog, c)
				s.CommitPoint++
				s.UserSeqs[c.Client]++

				s.Doc.apply(c, false)
				s.Doc.Seqs[c.Client]++
				s.Doc.View++

				if c.Type == Init {
					if xid, ok := s.UserSession[c.Client]; !ok || xid != c.View {
						s.UserViews[c.Client] = s.Doc.View // correct?
					}
				}
			}
			if s.UserViews[c.Client] < c.View && c.Type != Init {
				s.UserViews[c.Client] = c.View
			}
		}

		s.px.Done(seq)

		var min uint32
		for _, v := range s.UserViews {
			if min == 0 {
				min = v
			} else if min > v {
				min = v
			}
		}
		if s.DiscardPoint < min {
			s.CommitLog = s.CommitLog[min-s.DiscardPoint:]
			s.DiscardPoint = min
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
