package main

// Most of these are based on a stripped down version of kilo given by
// https://viewsourcecode.org/snaptoken/kilo/ as well as the editbox demo

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
	pushDelay = 1 * time.Second
	pullDelay = 1 * time.Second
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
	connection *rpc.Client
}

func StartClient(server string) {
	var gp gopad
	gp.id = rand.Uint32()
	gp.srv = server + Port
	gp.users = make(map[uint32]*Pos)

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
				gp.editorMoveCursor(ev.Key, &gp.pos, true)
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
				gp.editorDelRune(&gp.pos, gp.id, true)
			case termbox.KeyDelete, termbox.KeyCtrlD:
				gp.logOp([]Op{
					Op{Type: Move, Move: termbox.KeyArrowRight, View: gp.doc.View, Client: gp.id},
					Op{Type: Delete, View: gp.doc.View, Client: gp.id},
				})
				gp.editorMoveCursor(termbox.KeyArrowRight, &gp.pos, true)
				gp.editorDelRune(&gp.pos, gp.id, true)
			// case termbox.KeyTab:
			// 	edit_box.InsertRune('\t')
			case termbox.KeySpace:
				gp.logOp([]Op{Op{Type: Insert, Data: ' ', View: gp.doc.View, Client: gp.id}})
				gp.editorInsertRune(' ', &gp.pos, gp.id, true)
			case termbox.KeyEnter:
				gp.logOp([]Op{Op{Type: Newline, View: gp.doc.View, Client: gp.id}})
				gp.editorInsertNewLine(&gp.pos, gp.id, true)
			default:
				if ev.Ch != 0 {
					gp.logOp([]Op{Op{Type: Insert, Data: ev.Ch, View: gp.doc.View, Client: gp.id}})
					gp.editorInsertRune(ev.Ch, &gp.pos, gp.id, true)
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
			gp.status = string(reply.Err)
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
					temppos := *gp.users[gp.id]
					for _, op := range gp.selfOps {
						gp.apply(op, &temppos, true)
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
func (gp *gopad) apply(op Op, pos *Pos, temp bool) {
	switch op.Type {
	case Insert:
		gp.editorInsertRune(op.Data, pos, op.Client, temp)
	case Init:
		gp.users[op.Client] = &Pos{}
	case Move:
		gp.editorMoveCursor(op.Move, pos, temp)
	case Delete:
		gp.editorDelRune(pos, op.Client, temp)
	case Newline:
		gp.editorInsertNewLine(pos, op.Client, temp)
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

		gp.apply(op, gp.users[op.Client], false)
	}
	gp.doc.View += uint32(len(commits))

	// cut off commiteds
	if newpoint > gp.commitpoint {
		gp.selfOps = gp.selfOps[newpoint-gp.commitpoint:]
		gp.commitpoint = newpoint
	}
}

/*** input ***/

func (gp *gopad) editorMoveCursor(key termbox.Key, pos *Pos, temp bool) {
	var doc *Doc

	if temp {
		doc = &gp.tempdoc
	} else {
		doc = &gp.doc
	}

	var row *erow
	if pos.Y < doc.Numrows {
		row = &doc.Rows[pos.Y]
	} else {
		row = nil
	}

	switch key {
	case termbox.KeyArrowRight:
		if row != nil && pos.X < len(row.Chars) {
			pos.X++
		} else if row != nil && pos.X >= len(row.Chars) {
			pos.Y++
			pos.X = 0
		}
	case termbox.KeyArrowLeft:
		if pos.X != 0 {
			pos.X--
		} else if pos.Y > 0 {
			pos.Y--
			pos.X = len(doc.Rows[pos.Y].Chars)
		}
	case termbox.KeyArrowDown:
		if pos.Y < doc.Numrows {
			pos.Y++
		}
	case termbox.KeyArrowUp:
		if pos.Y != 0 {
			pos.Y--
		}
	case termbox.KeyHome:
		pos.X = 0
		return
	case termbox.KeyEnd:
		if pos.Y < doc.Numrows {
			pos.X = len(doc.Rows[pos.Y].Chars)
		}
		return
	}

	rowlen := 0
	if pos.Y < doc.Numrows {
		rowlen = len(doc.Rows[pos.Y].Chars)
	}
	if rowlen < 0 {
		rowlen = 0
	}
	if pos.X > rowlen {
		pos.X = rowlen
	}
}

/*** editor operations ***/

func (gp *gopad) editorInsertRune(key rune, pos *Pos, id uint32, temp bool) {
	var doc *Doc

	if temp {
		doc = &gp.tempdoc
	} else {
		doc = &gp.doc
	}

	if pos.Y == doc.Numrows {
		doc.insertRow(pos.Y, "", []bool{}, []uint32{})
	}

	doc.rowInsertRune(pos.X, pos.Y, key, id, temp)

	if !temp {
		for k, npos := range gp.users {
			if k != id {
				// update other positions
				if pos.Y == npos.Y && pos.X <= npos.X {
					npos.X++
					// if updating own pos then update temp pos also
					if k == gp.id {
						gp.pos.X++
					}
				}
			}
		}
	}

	pos.X++
}

func (gp *gopad) editorInsertNewLine(pos *Pos, id uint32, temp bool) {
	var doc *Doc
	if temp {
		doc = &gp.tempdoc
	} else {
		doc = &gp.doc
	}

	if pos.X == 0 {
		doc.insertRow(pos.Y, "", []bool{}, []uint32{})
	} else {
		row := &doc.Rows[pos.Y]
		doc.insertRow(pos.Y+1, row.Chars[pos.X:], row.Temp[pos.X:], row.Author[pos.X:])
		doc.Rows[pos.Y].Chars = row.Chars[:pos.X]
		doc.Rows[pos.Y].Temp = row.Temp[:pos.X]
		doc.Rows[pos.Y].Author = row.Author[:pos.X]
	}

	if !temp {
		for k, npos := range gp.users {
			if k != id {
				// update other positions
				if pos.Y == npos.Y && pos.X <= npos.X {
					npos.Y++
					npos.X -= pos.X
					// if updating own pos then update temp pos also
					if k == gp.id {
						gp.pos.Y++
						gp.pos.X -= pos.X
					}
				} else if pos.Y < npos.Y {
					npos.Y++
					// if updating own pos then update temp pos also
					if k == gp.id {
						gp.pos.Y++
					}
				}
			}
		}
	}
	pos.Y++
	pos.X = 0
}

func (gp *gopad) editorDelRune(pos *Pos, id uint32, temp bool) {
	var doc *Doc

	if temp {
		doc = &gp.tempdoc
	} else {
		doc = &gp.doc
	}

	if pos.Y == doc.Numrows {
		return
	}
	if pos.X == 0 && pos.Y == 0 {
		return
	}

	if pos.X > 0 {
		doc.rowDelRune(pos.X, pos.Y)
		if !temp {
			for k, npos := range gp.users {
				if k != id {
					// update other positions
					if pos.Y == npos.Y && pos.X <= npos.X {
						npos.X--
						// if updating own pos then update temp pos also
						if k == gp.id {
							gp.pos.X--
						}
					}
				}
			}
		}
		pos.X--
	} else {
		oldOffset := len(doc.Rows[pos.Y-1].Chars)
		doc.editorDelRow(pos.Y)

		if !temp {
			for k, npos := range gp.users {
				if k != id {
					// update other positions
					if pos.Y == npos.Y {
						npos.X += oldOffset
						npos.Y--
						// if updating own pos then update temp pos also
						if k == gp.id {
							gp.pos.X += oldOffset
							gp.pos.Y--
						}
					} else if pos.Y < npos.Y {
						npos.Y--
						// if updating own pos then update temp pos also
						if k == gp.id {
							gp.pos.Y--
						}
					}
				}
			}
		}

		pos.X = oldOffset
		pos.Y--
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
			termbox.SetCell(0, i, '~', coldef, coldef)
			x := 0
			for k, s := range gp.tempdoc.Rows[filerow].Chars {
				if k >= gp.coloff {
					if gp.tempdoc.Rows[filerow].Temp[k] {
						termbox.SetCell(x+1, i, s, 202, coldef)
					} else {
						auth := gp.tempdoc.Rows[filerow].Author[k]
						if auth == 0 || auth == gp.id {
							termbox.SetCell(x+1, i, s, coldef, coldef)
						} else {
							termbox.SetCell(x+1, i, s, 51, coldef)
						}
					}
					x++
					if x+1 > gp.screencols {
						break
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

	var j int

	for _, c := range gp.status {
		termbox.SetCell(j, i, c, termbox.ColorBlack, termbox.ColorWhite)
		j++
	}

	// draw status bar
	for ; j < gp.screencols+1; j++ {
		termbox.SetCell(j, i, ' ', termbox.ColorBlack, termbox.ColorWhite)
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
