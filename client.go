package main

// Most of these are based on a stripped down version of
// the text editor of https://viewsourcecode.org/snaptoken/kilo/ which
// is based on antirez's kilo

import (
	"bufio"
	"encoding/binary"
	"encoding/json"
	"fmt"
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
	pos        Pos
	doc        Doc
	tempdoc    Doc
	buf        *bufio.ReadWriter
	mu         sync.Mutex

	id          int
	selfOps     []Op
	opNum       int
	sentpoint   int
	commitpoint int

	users      map[int]*Pos
	connection *rpc.Client
}

func StartClient(server string) {
	var gp gopad
	gp.id = rand.Int()
	gp.srv = server + Port
	gp.users = make(map[int]*Pos)
	gp.users[gp.id] = &Pos{}

	gp.editorOpen(server+Port, -1)

	err := termbox.Init()
	if err != nil {
		panic(err)
	}
	defer termbox.Close()
	termbox.SetInputMode(termbox.InputEsc)

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
				gp.connection.Close()
				break mainloop
			case termbox.KeyArrowLeft, termbox.KeyArrowRight, termbox.KeyArrowUp, termbox.KeyArrowDown:
				gp.editorMoveCursor(ev.Key)
			case termbox.KeyPgup, termbox.KeyPgdn:
				for times := gp.screenrows; times > 0; times-- {
					var x termbox.Key
					if ev.Key == termbox.KeyPgdn {
						x = termbox.KeyArrowDown
					} else {
						x = termbox.KeyArrowUp
					}
					gp.editorMoveCursor(x)
				}
			case termbox.KeyHome:
				gp.pos.X = 0
			case termbox.KeyEnd:
				if gp.pos.Y < gp.doc.Numrows {
					gp.pos.X = len(gp.doc.Rows[gp.pos.Y].Chars)
				}
			case termbox.KeyBackspace, termbox.KeyBackspace2:
				gp.editorDelRune(true)
			case termbox.KeyDelete, termbox.KeyCtrlD:
				gp.editorMoveCursor(termbox.KeyArrowRight)
				gp.editorDelRune(true)
			// case termbox.KeyTab:
			// 	edit_box.InsertRune('\t')
			case termbox.KeySpace:
				gp.editorInsertRune(' ', &gp.pos, true, true)
			case termbox.KeyEnter:
				gp.editorInsertNewLine(true)
			default:
				if ev.Ch != 0 {
					gp.editorInsertRune(ev.Ch, &gp.pos, true, true)
				}
			}
		case termbox.EventError:
			panic(ev.Err)
		}
		gp.refreshScreen()
	}
}

// rpc caller
func (gp *gopad) call(srv string, rpcname string, args interface{}, reply interface{}) bool {
	var err error
	if gp.connection == nil {
		// attempt to dial
		gp.connection, err = rpc.Dial("tcp", srv)
		if err != nil {
			log.Println("Couldn't connect to", srv)
			return false
		}
	}

	err = gp.connection.Call(rpcname, args, reply)
	if err != nil {
		log.Println(err)
		return false
	}
	return true
}

// push commits to server
func (gp *gopad) push() {
	for {
		gp.mu.Lock()
		if len(gp.selfOps)+gp.commitpoint <= gp.sentpoint {
			// no new ops
			gp.mu.Unlock()
			time.Sleep(pushDelay)
			continue
		}
		// prepare and send new ops
		buf, err := json.Marshal(gp.selfOps[gp.sentpoint-gp.commitpoint : len(gp.selfOps)])
		if err != nil {
			log.Println("Couldn't marshal commits", err)
			time.Sleep(pushDelay)
			continue
		}

		arg := Arg{Op: "Op", Data: buf}

		ok := false
		for !ok {
			var reply Reply
			ok = gp.call(gp.srv, "Server.Handle", arg, &reply)
			if ok {
				if reply.Err == "OK" {
					// update op sent point
					gp.sentpoint = len(gp.selfOps) + gp.commitpoint
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
		buf := make([]byte, binary.MaxVarintLen64)
		binary.LittleEndian.PutUint32(buf, gp.doc.View)

		for !ok {
			var reply Reply
			ok = gp.call(gp.srv, "Server.Handle", Arg{Op: "Query", Data: buf}, &reply)
			if ok && reply.Err == "OK" {
				var commits []Op
				json.Unmarshal(reply.Data, &commits)
				if len(commits) > 0 {
					// apply commited ops
					gp.mu.Lock()
					gp.applyOps(commits)
					gp.mu.Unlock()

					// apply ops not yet commited
					gp.tempdoc = *gp.doc.copy()
					temppos := *gp.users[gp.id]
					for _, op := range gp.selfOps {
						gp.apply(op, &temppos, true)
					}

					gp.refreshScreen()
				}
			} else {
				time.Sleep(pullDelay)
			}
		}
		time.Sleep(pullDelay)
	}
}

// TODO
func (gp *gopad) apply(op Op, pos *Pos, temp bool) {
	switch op.Op {
	case Insert:
		gp.editorInsertRune(op.Data, pos, temp, false)
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
		// TODO: update gp.doc
		gp.apply(op, gp.users[op.Client], false)
	}

	// cut off commiteds
	if newpoint > gp.commitpoint {
		gp.selfOps = gp.selfOps[newpoint-gp.commitpoint:]
		gp.commitpoint = newpoint
	}
	gp.doc.View += uint32(len(commits))
}

/*** editor operations ***/

func (gp *gopad) editorInsertRune(key rune, pos *Pos, temp, log bool) {
	var doc *Doc

	if temp {
		doc = &gp.tempdoc
	} else {
		doc = &gp.doc
	}

	if log {
		gp.mu.Lock()
		gp.opNum++
		gp.selfOps = append(gp.selfOps, Op{Op: Insert, X: gp.pos.X,
			Y: gp.pos.Y, ID: gp.opNum, Data: key, View: gp.doc.View, Client: gp.id})
		gp.mu.Unlock()
		pos = &gp.pos
	}

	if pos.Y == doc.Numrows {
		doc.insertRow(pos.Y, "", []bool{})
	}

	doc.rowInsertRune(pos.X, pos.Y, key, temp)
	pos.X++
}

func (gp *gopad) editorInsertNewLine(temp bool) {
	var doc *Doc
	if temp {
		gp.mu.Lock()
		gp.selfOps = append(gp.selfOps, Op{Op: Newline, X: gp.pos.X,
			Y: gp.pos.Y, ID: gp.opNum, View: gp.doc.View, Client: gp.id})
		gp.opNum++
		gp.mu.Unlock()
		doc = &gp.tempdoc
	} else {
		doc = &gp.doc
	}

	if gp.pos.X == 0 {
		doc.insertRow(gp.pos.Y, "", []bool{})
	} else {
		row := &doc.Rows[gp.pos.Y]
		doc.insertRow(gp.pos.Y+1, row.Chars[gp.pos.X:], row.Temp[gp.pos.X:])
		row.Chars = row.Chars[:gp.pos.X]
	}
	gp.pos.Y++
	gp.pos.X = 0
}

func (gp *gopad) editorDelRune(temp bool) {
	var doc *Doc
	if temp {
		// write operation to self commit log
		gp.mu.Lock()
		gp.selfOps = append(gp.selfOps, Op{Op: Delete, X: gp.pos.X,
			Y: gp.pos.Y, ID: gp.opNum, View: gp.doc.View, Client: gp.id})
		gp.opNum++
		gp.mu.Unlock()
		doc = &gp.tempdoc
	} else {
		doc = &gp.doc
	}

	if gp.pos.Y == doc.Numrows {
		return
	}
	if gp.pos.X == 0 && gp.pos.Y == 0 {
		return
	}

	if gp.pos.X > 0 {
		doc.rowDelRune(gp.pos.X, gp.pos.Y)
		gp.pos.X--
	} else {
		gp.pos.X = len(gp.doc.Rows[gp.pos.Y-1].Chars)
		doc.editorDelRow(gp.pos.Y)
		gp.pos.Y--
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
						termbox.SetCell(x+1, i, s, termbox.ColorRed, coldef)
					} else {
						termbox.SetCell(x+1, i, s, coldef, coldef)
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

	var j int

	for _, c := range fmt.Sprintf("%d", gp.users[gp.id].X) {
		termbox.SetCell(j, i, c, termbox.ColorBlack, termbox.ColorWhite)
		j++
	}

	// draw status bar
	for ; j < gp.screencols+1; j++ {
		termbox.SetCell(j, i, '>', termbox.ColorBlack, termbox.ColorWhite)
	}

}

func (gp *gopad) refreshScreen() {
	gp.mu.Lock()
	const coldef = termbox.ColorDefault
	termbox.Clear(coldef, coldef)

	gp.editorScroll()
	gp.drawRows()

	termbox.SetCursor(gp.pos.X-gp.coloff+1, gp.pos.Y-gp.rowoff)
	termbox.Flush()
	gp.mu.Unlock()
}

/*** file i/o ***/

// get file from server
func (gp *gopad) editorOpen(server string, view int) {
	var reply Reply
	ok := gp.call(server, "Server.Handle", Arg{Op: "Init"}, &reply)
	if ok {
		var d Doc
		err := json.Unmarshal(reply.Data, &d)
		if err != nil {
			log.Fatal("Couldn't decode document")
		}

		for _, row := range d.Rows {
			gp.doc.insertRow(gp.doc.Numrows, row.Chars, row.Temp)
		}
	} else {
		log.Fatal("Not ok call")
	}
	gp.tempdoc = *gp.doc.copy()
}

/*** input ***/

func (gp *gopad) editorMoveCursor(key termbox.Key) {
	var row *erow
	if gp.pos.Y < gp.tempdoc.Numrows {
		row = &gp.tempdoc.Rows[gp.pos.Y]
	} else {
		row = nil
	}

	switch key {
	case termbox.KeyArrowRight:
		if row != nil && gp.pos.X < len(row.Chars) {
			gp.pos.X++
		} else if row != nil && gp.pos.X >= len(row.Chars) {
			gp.pos.Y++
			gp.pos.X = 0
		}
	case termbox.KeyArrowLeft:
		if gp.pos.X != 0 {
			gp.pos.X--
		} else if gp.pos.Y > 0 {
			gp.pos.Y--
			gp.pos.X = len(gp.tempdoc.Rows[gp.pos.Y].Chars)
		}
	case termbox.KeyArrowDown:
		if gp.pos.Y < gp.tempdoc.Numrows {
			gp.pos.Y++
		}
	case termbox.KeyArrowUp:
		if gp.pos.Y != 0 {
			gp.pos.Y--
		}
	}

	rowlen := 0
	if gp.pos.Y < gp.tempdoc.Numrows {
		rowlen = len(gp.tempdoc.Rows[gp.pos.Y].Chars)
	}
	if rowlen < 0 {
		rowlen = 0
	}
	if gp.pos.X > rowlen {
		gp.pos.X = rowlen
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
