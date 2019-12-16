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

type ViewSeq struct {
	View uint32
	Seq  int
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
	// Doc.UserSession  map[int]uint32 // xid of current user session
	doc          Doc
	CommitLog    []Op
	UserViews    map[int]uint32 // last reported view number by user
	CommitPoint  uint32         // the upper bound of our commit log (in absolute terms)
	DiscardPoint uint32         // ops below this have been discarded
	StartSeq     int            // seq number to Start paxos
	QuerySeq     int            // seq number to Query paxos
	ViewSeqs     []ViewSeq      // Paxos seqs -> Doc Views

	// handler  map[string]HandleFunc
	// m sync.RWMutex
}

func (s *Server) Copy(args *RecoverArg, reply *RecoverReply) error {
	if !s.reboot {
		s.mu.Lock()
		s.px.Lock()

		reply.Srv, _ = json.Marshal(s)
		reply.Px, _ = json.Marshal(s.px)
		reply.Doc, _ = docToBytes(&s.doc)

		reply.Err = "OK"

		s.px.Unlock()
		s.mu.Unlock()
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
			if i != s.me {
				ok := call(srv, "Server.Copy", RecoverArg{}, &reply, false)
				if ok && reply.Err == "OK" {
					json.Unmarshal(reply.Srv, &tmp)
					s.px.Recover(reply.Px)
					done = true
					break
				}
			}
			time.Sleep(updateDelay)
		}
	}

	s.CommitLog = tmp.CommitLog
	s.UserViews = tmp.UserViews
	s.CommitPoint = tmp.CommitPoint
	s.DiscardPoint = tmp.DiscardPoint
	s.StartSeq = tmp.StartSeq
	s.QuerySeq = tmp.QuerySeq
	s.ViewSeqs = tmp.ViewSeqs

	bytesToDoc(reply.Doc, &s.doc)
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
		s.UserViews = make(map[int]uint32)
		s.CommitLog = make([]Op, 0)
		s.ViewSeqs = make([]ViewSeq, 0)

		// new document, so start fresh
		s.doc = Doc{
			UserSeqs:    make(map[int]uint32),
			UserPos:     make(map[int]Pos),
			Colors:      make(map[int]int),
			UserSession: make(map[int]uint32),
		}

		if fname != "" {
			file, err := os.Open(fname)
			if err != nil {
				log.Fatal(err)
			}
			defer file.Close()
			scanner := bufio.NewScanner(file)
			for scanner.Scan() {
				s.doc.Rows = append(s.doc.Rows,
					erow{
						Chars:  scanner.Text(),
						Temp:   make([]bool, len(scanner.Text())),
						Author: make([]int, len(scanner.Text())),
					})
			}
		} else {
			// empty first row
			s.doc.Rows = append(s.doc.Rows,
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
		s.px.Start(s.StartSeq, Paxage{ops, xid})
		pkg := s.getOp(s.StartSeq)
		s.StartSeq++

		if pkg.Xid == xid {
			break
		}
	}
}

func (s *Server) Init(arg InitArg, reply *InitReply) error {
	log.Println("Sending initial...", arg.Client)
	s.mu.Lock()
	session, ok := s.doc.UserSession[arg.Client]

	if !ok && len(s.doc.Colors) >= MAXUSERS {
		reply.Err = "Full"
		s.mu.Unlock()
		return nil
	}

	if session != arg.Session {
		// new session
		s.handleOp([]Op{Op{Type: Init, Session: arg.Session, Client: arg.Client}})

		// marshal document and send back
		buf, err := docToBytes(&s.doc)
		if err != nil {
			log.Println("Couldn't send document", err)
			reply.Err = "Encode"
			return nil
		}
		reply.Doc = buf
		reply.Err = "OK"
	} else {
		reply.Err = "Duplicate"
	}

	s.mu.Unlock()
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
	if ops[0].Seq > s.doc.UserSeqs[ops[0].Client]+1 {
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

	if ops[len(ops)-1].Seq > s.doc.UserSeqs[ops[0].Client] {
		// there is a new op
		s.handleOp(ops)
	}

	reply.Err = "OK"
	return nil
}

// apply log
func (s *Server) update() {
	var ops []Op
	for {
		status, val := s.px.status(s.QuerySeq)
		if status == Pending {
			time.Sleep(updateDelay)
			continue
		}

		// get package from paxos
		ops = (val.(Paxage)).Payload.([]Op)
		s.mu.Lock()

		var viewMax uint32

		// append to commit log
		for _, c := range ops {
			if s.doc.apply(c, false) {
				// append to commitlog if op is applicable
				s.CommitLog = append(s.CommitLog, c)
				s.CommitPoint++
			}
			if s.UserViews[c.Client] < c.View && c.Type != Init {
				// only update UserView if not Init
				s.UserViews[c.Client] = c.View
			}
			if c.View > viewMax {
				viewMax = c.View
			}
		}

		s.ViewSeqs = append(s.ViewSeqs, ViewSeq{View: viewMax, Seq: s.QuerySeq})
		s.QuerySeq++
		if s.StartSeq < s.QuerySeq {
			s.StartSeq = s.QuerySeq
		}

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

		s.processDone(min)

		s.mu.Unlock()
		time.Sleep(updateDelay)
	}
}

func (s *Server) processDone(view uint32) {
	for {
		if len(s.ViewSeqs) == 0 {
			break
		} else {
			if s.ViewSeqs[0].View <= view {
				s.px.Done(s.ViewSeqs[0].Seq)
				s.ViewSeqs = s.ViewSeqs[1:]
			} else {
				break
			}
		}
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
