package main

// Most of these are based on a version of antirez's kilo given by
// https://viewsourcecode.org/snaptoken/kilo/ as well as the editbox demo of termbox-go

import (
	"bufio"
	"encoding/json"
	"github.com/nsf/termbox-go"
	"log"
	"math/rand"
	"net/rpc"
	"sync"
	"time"
)

const (
	pushDelay = 200 * time.Millisecond
	pullDelay = 200 * time.Millisecond
	// pushDelay = 1 * time.Second
	// pullDelay = 1 * time.Second
)

type gopad struct {
	// rx         int
	srv        string
	screenrows int
	screencols int
	rowoff     int
	coloff     int
	pos        Pos // current position (possibly not commited)
	doc        Doc
	tempdoc    Doc
	buf        *bufio.ReadWriter
	mu         sync.Mutex

	status      string
	id          uint32
	selfOps     []Op
	opNum       uint32
	sentpoint   uint32
	commitpoint uint32

	users      map[uint32]*Pos // last known position of other users
	tempUsers  map[uint32]*Pos
	userColors map[uint32]int
	connection *rpc.Client
	numusers   int
}

func StartClient(server string) {
	var gp gopad
	gp.id = rand.Uint32()
	gp.srv = server + Port
	gp.users = make(map[uint32]*Pos)
	gp.tempUsers = make(map[uint32]*Pos)
	gp.userColors = make(map[uint32]int)

	gp.userColors[gp.id] = 0

	gp.editorOpen(server+Port, 0)

	err := termbox.Init()
	if err != nil {
		panic(err)
	}
	defer termbox.Close()
	termbox.SetInputMode(termbox.InputEsc)
	termbox.SetOutputMode(termbox.Output256)

	gp.initEditor()

	go gp.push()
	go gp.pull()

	gp.refreshScreen()
mainloop:
	for {
		switch ev := termbox.PollEvent(); ev.Type {
		case termbox.EventKey:
			switch ev.Key {
			case termbox.KeyCtrlC:
				break mainloop
			case termbox.KeyArrowLeft,
				termbox.KeyArrowRight,
				termbox.KeyArrowUp,
				termbox.KeyArrowDown,
				termbox.KeyHome,
				termbox.KeyEnd:
				gp.logOp([]Op{Op{Type: Move, Move: ev.Key, View: gp.doc.View, Client: gp.id}})
				editorMoveCursor(&gp.pos, &gp.tempdoc, ev.Key)
			// case termbox.KeyPgup, termbox.KeyPgdn:
			// 	for times := gp.screenrows; times > 0; times-- {
			// 		var x termbox.Key
			// 		if ev.Key == termbox.KeyPgdn {
			// 			x = termbox.KeyArrowDown
			// 		} else {
			// 			x = termbox.KeyArrowUp
			// 		}
			// 		gp.editorMoveCursor(x, &gp.pos, true, true)
			// 	}
			case termbox.KeyBackspace, termbox.KeyBackspace2:
				gp.logOp([]Op{Op{Type: Delete, View: gp.doc.View, Client: gp.id}})
				editorDelRune(&gp.pos, &gp.tempdoc)
			case termbox.KeyDelete, termbox.KeyCtrlD:
				gp.logOp([]Op{
					Op{Type: Move, Move: termbox.KeyArrowRight, View: gp.doc.View, Client: gp.id},
					Op{Type: Delete, View: gp.doc.View, Client: gp.id},
				})
				editorMoveCursor(&gp.pos, &gp.tempdoc, termbox.KeyArrowRight)
				editorDelRune(&gp.pos, &gp.tempdoc)
			// case termbox.KeyTab:
			// 	edit_box.InsertRune('\t')
			case termbox.KeySpace:
				op := Op{Type: Insert, Data: ' ', View: gp.doc.View, Client: gp.id}
				gp.logOp([]Op{op})
				editorInsertRune(&gp.pos, &gp.tempdoc, ' ', gp.id, true)
				// gp.apply(op, &gp.pos, &gp.tempdoc, true)
			case termbox.KeyEnter:
				gp.logOp([]Op{Op{Type: Newline, View: gp.doc.View, Client: gp.id}})
				editorInsertNewLine(&gp.pos, &gp.tempdoc)
			default:
				if ev.Ch != 0 {
					gp.logOp([]Op{Op{Type: Insert, Data: ev.Ch, View: gp.doc.View, Client: gp.id}})
					editorInsertRune(&gp.pos, &gp.tempdoc, ev.Ch, gp.id, true)
				}
			}
		case termbox.EventError:
			panic(ev.Err)
		}
		gp.refreshScreen()
	}
}

func (gp *gopad) logOp(ops []Op) {
	gp.mu.Lock()
	for _, op := range ops {
		gp.opNum++
		op.ID = gp.opNum
		gp.selfOps = append(gp.selfOps, op)
	}
	gp.mu.Unlock()
}

// push commits to server
func (gp *gopad) push() {
	for {
		gp.mu.Lock()
		l := uint32(len(gp.selfOps)) + gp.commitpoint
		if l <= gp.sentpoint {
			// no new ops
			gp.mu.Unlock()
			time.Sleep(pushDelay)
			continue
		}
		// prepare and send new ops
		buf, err := json.Marshal(gp.selfOps[gp.sentpoint-gp.commitpoint : len(gp.selfOps)])
		if err != nil {
			log.Println("Couldn't marshal commits", err)
			gp.mu.Unlock()
			time.Sleep(pushDelay)
			continue
		}
		gp.mu.Unlock()

		ok := false
		for !ok {
			var reply OpReply
			ok = call(gp.srv, "Server.Handle", OpArg{Data: buf}, &reply, false)
			if ok {
				gp.mu.Lock()
				if reply.Err == "OK" {
					// update op sent point
					gp.sentpoint = l
				}
				gp.mu.Unlock()
			}
		}
		time.Sleep(pushDelay)
	}
}

// pulls commited operations from server
func (gp *gopad) pull() {
	for {
		ok := false

		for !ok {
			var reply QueryReply
			ok = call(gp.srv, "Server.Query", QueryArg{View: gp.doc.View}, &reply, false)
			if ok && reply.Err == "OK" {
				var commits []Op
				json.Unmarshal(reply.Data, &commits)
				if len(commits) > 0 {
					// apply commited ops
					gp.mu.Lock()
					gp.applyOps(commits)
					gp.tempdoc = *gp.doc.copy()

					// apply ops not yet commited
					for k, v := range gp.users {
						gp.tempUsers[k] = v
					}
					gp.pos = *gp.users[gp.id]
					for _, op := range gp.selfOps {
						gp.apply(op, &gp.pos, &gp.tempdoc, true)
					}
					gp.mu.Unlock()

					gp.refreshScreen()
				}
			} else {
				time.Sleep(pullDelay)
			}
		}
		time.Sleep(pullDelay)
	}
}

// Update commited ops
func (gp *gopad) apply(op Op, pos *Pos, doc *Doc, temp bool) {
	switch op.Type {
	case Insert:
		editorInsertRune(pos, doc, op.Data, op.Client, temp)
	case Init:
		gp.users[op.Client] = &Pos{}
		gp.tempUsers[op.Client] = &Pos{}
		gp.numusers++
		// if op.Client != gp.id {
		gp.userColors[op.Client] = gp.numusers
		// }
	case Move:
		editorMoveCursor(pos, doc, op.Move)
	case Delete:
		editorDelRune(pos, doc)
	case Newline:
		editorInsertNewLine(pos, doc)
	}
}

// apply commits to doc
func (gp *gopad) applyOps(commits []Op) {

	newpoint := gp.commitpoint

	for _, op := range commits {
		// update commitpoint
		if op.Client == gp.id {
			newpoint = op.ID
		}

		var pos Pos
		var oldOffset int
		if op.Type != Init {
			pos = *(gp.users[op.Client])
			if op.Type == Delete && pos.Y > 0 {
				oldOffset = len(gp.doc.Rows[pos.Y-1].Chars)
			}
		}

		gp.apply(op, gp.users[op.Client], &gp.doc, false)

		if op.Type != Move && op.Type != Init {
			for k, npos := range gp.users {
				// update other positions
				if k != op.Client {
					updatePos(pos, npos, op.Type, oldOffset)

					if k == gp.id {
						// if updating self, then from someone else so update temp pos also
						updatePos(pos, &gp.pos, op.Type, oldOffset)
					}
				}
			}
		}
	}
	gp.doc.View += uint32(len(commits))

	// cut off commiteds
	if newpoint > gp.commitpoint {
		gp.selfOps = gp.selfOps[newpoint-gp.commitpoint:]
		gp.commitpoint = newpoint
	}
}

/*** output ***/

func (gp *gopad) editorScroll() {
	// gp.rx = 0
	// if gp.pos.Y < gp.doc.Numrows {
	// 	gp.rx = editorRowCxToRx(&gp.doc.Rows[gp.pos.Y], gp.pos.X)
	// }

	// reposition up
	if gp.pos.Y < gp.rowoff {
		gp.rowoff = gp.pos.Y
	}

	// reposition down
	if gp.pos.Y >= gp.rowoff+gp.screenrows {
		gp.rowoff = gp.pos.Y - gp.screenrows + 1
	}

	if gp.pos.X < gp.coloff {
		gp.coloff = gp.pos.X
	}

	if gp.pos.X >= gp.coloff+gp.screencols {
		gp.coloff = gp.pos.X - gp.screencols + 1
	}
}

// draw a row
func (gp *gopad) drawRows() {
	const coldef = termbox.ColorDefault

	i := 0
	for ; i < gp.screenrows; i++ {
		filerow := i + gp.rowoff
		if filerow < gp.tempdoc.Numrows {

			// draw gutters
			termbox.SetCell(0, i, '~', coldef, coldef)

			if len(gp.tempdoc.Rows[filerow].Chars) > 0 {

				for k, s := range gp.tempdoc.Rows[filerow].Chars {

					if k >= gp.coloff {

						bg := termbox.ColorDefault

						// draw other cursors
						for user, pos := range gp.users {
							if user != gp.id {
								if pos.X == k && pos.Y == filerow {
									bg = CURSORS[gp.userColors[user]]
								}
							}
						}

						if gp.tempdoc.Rows[filerow].Temp[k] {
							// is temp char?
							termbox.SetCell(k+1, i, s, 251, bg)
						} else {

							// select color based on author
							auth := gp.tempdoc.Rows[filerow].Author[k]
							color := COLORS[gp.userColors[auth]]
							termbox.SetCell(k+1, i, s, color, bg)
						}
						if k+1 > gp.screencols {
							break
						}
					}
				}

				end := len(gp.tempdoc.Rows[filerow].Chars)

				if end >= gp.coloff && end < gp.coloff+gp.screencols {
					for user, pos := range gp.users {
						if user != gp.id {
							if pos.X == end && pos.Y == filerow {
								termbox.SetCell(end-gp.coloff+1, i, ' ', 0, CURSORS[gp.userColors[user]])
							}
						}
					}
				}
			} else if gp.coloff == 0 {
				// draw cursor on empty line
				for user, pos := range gp.users {
					if user != gp.id {
						if pos.Y == filerow {
							termbox.SetCell(1, i, ' ', 0, CURSORS[gp.userColors[user]])
						}
					}
				}
			}
		} else {
			termbox.SetCell(0, i, '~', coldef, coldef)
		}
	}
}

func (gp *gopad) editorDrawStatusBar() {
	i := gp.screenrows

	bg := termbox.ColorWhite

	if gp.userColors[gp.id] != 0 {
		bg = COLORS[gp.userColors[gp.id]]
	}
	var j int

	for _, c := range gp.status {
		termbox.SetCell(j, i, c, termbox.ColorBlack, bg)
		j++
	}

	// draw status bar
	for ; j < gp.screencols+1; j++ {
		termbox.SetCell(j, i, ' ', termbox.ColorBlack, bg)
	}

}

func (gp *gopad) refreshScreen() {
	gp.mu.Lock()
	const coldef = termbox.ColorDefault
	termbox.Clear(coldef, coldef)

	gp.editorScroll()
	gp.drawRows()
	gp.editorDrawStatusBar()

	termbox.SetCursor(gp.pos.X-gp.coloff+1, gp.pos.Y-gp.rowoff)
	termbox.Flush()
	gp.mu.Unlock()
}

/*** file i/o ***/

// get file from server
func (gp *gopad) editorOpen(server string, view uint32) {
	var reply InitReply
	ok := call(server, "Server.Init", InitArg{Client: gp.id, View: view}, &reply, false)
	if ok {
		var d Doc
		err := json.Unmarshal(reply.Doc, &d)
		if err != nil {
			log.Fatal("Couldn't decode document")
		}

		var c []Op
		err = json.Unmarshal(reply.Commits, &c)
		if err != nil {
			log.Fatal("Couldn't decode document")
		}

		for _, row := range d.Rows {
			gp.doc.insertRow(gp.doc.Numrows, row.Chars, row.Temp, row.Author)
		}

		if len(c) > 0 {
			gp.applyOps(c)
		}

		gp.tempdoc = *gp.doc.copy()
	} else {
		log.Fatal("Not ok call", false)
	}
}

/*** init ***/

func (gp *gopad) initEditor() {
	gp.screencols, gp.screenrows = termbox.Size()
	gp.screenrows--
	gp.screencols--
}

// func editorRowCxToRx(row *erow, pos.X int) int {
// 	rx := 0
// 	for j := 0; j < pos.X; j++ {
// 		if row.Chars[j] == '\t' {
// 			rx += (TABSTOP - 1) - (rx % TABSTOP)
// 		}
// 		rx++
// 	}
// 	return rx
// }

// func renderRow(s string) string {
// 	var sb strings.Builder
// 	x := 0
// 	for _, r := range s {
// 		// TODO: fix tabs
// 		if r == '\t' {
// 			for {
// 				sb.WriteRune(' ')
// 				x++
// 				if x%TABSTOP == 0 {
// 					break
// 				}
// 			}
// 		} else {
// 			sb.WriteRune(r)
// 			x++
// 		}
// 	}
// 	return sb.String()
// }

// rpc caller
// func call(srv string, rpcname string, args interface{}, reply interface{}) bool {
// 	// attempt to dial
// 	c, err := rpc.Dial("tcp", srv)
// 	if err != nil {
// 		// log.Println("Couldn't connect to", srv)
// 		return false
// 	}
// 	defer c.Close()

// 	err = c.Call(rpcname, args, reply)
// 	if err != nil {
// 		// log.Println(err)
// 		return false
// 	}
// 	return true
// }
