package main

// Most of these are based on a stripped down version of
// the text editor of https://viewsourcecode.org/snaptoken/kilo/ which
// is based on antirez's kilo

import (
	"bufio"
	"encoding/binary"
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
	cx         int
	cy         int
	doc        Doc
	buf        *bufio.ReadWriter
	mu         sync.Mutex
	id         int64
	selfOps    []Op
	opNum      int
	sentpoint  int
}

func StartClient(server string) {
	var gp gopad
	gp.id = rand.Int63()
	gp.srv = server + Port

	gp.editorOpen(server + Port)

	err := termbox.Init()
	if err != nil {
		panic(err)
	}
	defer termbox.Close()
	termbox.SetInputMode(termbox.InputEsc)

	gp.initEditor()

	go gp.push()

	gp.refreshScreen()
mainloop:
	for {
		switch ev := termbox.PollEvent(); ev.Type {
		case termbox.EventKey:
			switch ev.Key {
			case termbox.KeyCtrlC:
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
				gp.cx = 0
			case termbox.KeyEnd:
				if gp.cy < gp.doc.Numrows {
					gp.cx = len(gp.doc.Rows[gp.cy].Chars)
				}
			case termbox.KeyBackspace, termbox.KeyBackspace2:
				gp.editorDelRune()
			case termbox.KeyDelete, termbox.KeyCtrlD:
				gp.editorMoveCursor(termbox.KeyArrowRight)
				gp.editorDelRune()
			// case termbox.KeyTab:
			// 	edit_box.InsertRune('\t')
			case termbox.KeySpace:
				gp.sendRune(' ')
			case termbox.KeyEnter:
				gp.editorInsertNewLine()
			default:
				if ev.Ch != 0 {
					gp.sendRune(ev.Ch)
				}
			}
		case termbox.EventError:
			panic(ev.Err)
		}
		gp.refreshScreen()
	}
}

func (gp *gopad) sendRune(r rune) {
	gp.mu.Lock()
	gp.selfOps = append(gp.selfOps, Op{Op: "Insert", X: gp.cx,
		Y: gp.cy, ID: gp.opNum, Data: r, View: gp.doc.View})
	gp.opNum++
	gp.mu.Unlock()
	gp.editorInsertRune(r, true)
}

// rpc caller
func call(srv string, rpcname string, args interface{}, reply interface{}) bool {
	c, errx := rpc.Dial("tcp", srv)
	if errx != nil {
		log.Println("Couldn't connect to", srv)
		return false
	}
	defer c.Close()

	err := c.Call(rpcname, args, reply)
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
		if len(gp.selfOps) <= gp.sentpoint {
			// no new ops
			gp.mu.Unlock()
			time.Sleep(pushDelay)
			continue
		}
		// prepare and send new ops
		buf, err := json.Marshal(gp.selfOps[gp.sentpoint:len(gp.selfOps)])
		newpoint := len(gp.selfOps)
		gp.mu.Unlock()
		if err != nil {
			log.Println("Couldn't marshal commits", err)
			time.Sleep(pushDelay)
			continue
		}

		arg := Arg{Op: "Op", Data: buf}

		ok := false
		for !ok {
			var reply Reply
			ok = call(gp.srv, "Server.Handle", arg, &reply)
			if ok && reply.Err == "OK" {
				// update op sent point
				gp.mu.Lock()
				gp.sentpoint = newpoint
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
		var buf []byte
		binary.PutVarint(buf, int64(gp.doc.View))
		for !ok {
			var reply Reply
			ok = call(gp.srv, "Server.Handle", Arg{Op: "Query", Data: buf}, &reply)
			if ok && reply.Err == "OK" {
				var commits []Op
				json.Unmarshal(reply.Data, &commits)
				if len(commits) > 0 {
					gp.mu.Lock()
					gp.applyOps(commits)
					gp.mu.Unlock()
				}
			} else {
				time.Sleep(pullDelay)
			}
		}
		time.Sleep(pullDelay)
	}
}

// apply commits to doc
func (gp *gopad) applyOps(commits []Op) {
}

/*** file i/o ***/

// get file from server
func (gp *gopad) editorOpen(server string) {
	var reply Reply
	ok := call(server, "Server.Handle", Arg{Op: "Init"}, &reply)
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
}

/*** editor operations ***/

func (gp *gopad) editorInsertRune(key rune, temp bool) {
	if gp.cy == gp.doc.Numrows {
		gp.doc.insertRow(gp.cy, "", []bool{})
	}

	gp.doc.rowInsertRune(gp.cx, gp.cy, key, temp)
	gp.cx++
}

func (gp *gopad) editorInsertNewLine() {
	if gp.cx == 0 {
		gp.doc.insertRow(gp.cy, "", []bool{})
	} else {
		row := &gp.doc.Rows[gp.cy]
		gp.doc.insertRow(gp.cy+1, row.Chars[gp.cx:], row.Temp[gp.cx:])
		row.Chars = row.Chars[:gp.cx]
	}
	gp.cy++
	gp.cx = 0
}

func (gp *gopad) editorDelRune() {
	if gp.cy == gp.doc.Numrows {
		return
	}
	if gp.cx == 0 && gp.cy == 0 {
		return
	}

	if gp.cx > 0 {
		gp.doc.rowDelRune(gp.cx, gp.cy)
		gp.cx--
	} else {
		gp.cx = len(gp.doc.Rows[gp.cy-1].Chars)
		gp.doc.editorDelRow(gp.cy)
		gp.cy--
	}
}

/*** output ***/

func (gp *gopad) editorScroll() {
	// gp.rx = 0
	// if gp.cy < gp.doc.Numrows {
	// 	gp.rx = editorRowCxToRx(&gp.doc.Rows[gp.cy], gp.cx)
	// }

	// reposition up
	if gp.cy < gp.rowoff {
		gp.rowoff = gp.cy
	}

	// reposition down
	if gp.cy >= gp.rowoff+gp.screenrows {
		gp.rowoff = gp.cy - gp.screenrows + 1
	}

	if gp.cx < gp.coloff {
		gp.coloff = gp.cx
	}

	if gp.cx >= gp.coloff+gp.screencols {
		gp.coloff = gp.cx - gp.screencols + 1
	}
}

// draw a row
func (gp *gopad) drawRows() {
	const coldef = termbox.ColorDefault

	i := 0
	for ; i < gp.screenrows; i++ {
		filerow := i + gp.rowoff
		if filerow < gp.doc.Numrows {
			termbox.SetCell(0, i, '~', coldef, coldef)
			x := 0
			for k, s := range gp.doc.Rows[filerow].Chars {
				if k >= gp.coloff {
					if gp.doc.Rows[filerow].Temp[k] {
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

	// draw status bar
	for j := 0; j < gp.screencols+1; j++ {
		termbox.SetCell(j, i, '>', termbox.ColorBlack, termbox.ColorWhite)
	}

}

func (gp *gopad) refreshScreen() {
	gp.mu.Lock()
	const coldef = termbox.ColorDefault
	termbox.Clear(coldef, coldef)

	gp.editorScroll()
	gp.drawRows()

	termbox.SetCursor(gp.cx-gp.coloff+1, gp.cy-gp.rowoff)
	termbox.Flush()
	gp.mu.Unlock()
}

/*** input ***/

func (gp *gopad) editorMoveCursor(key termbox.Key) {
	var row *erow
	if gp.cy < gp.doc.Numrows {
		row = &gp.doc.Rows[gp.cy]
	} else {
		row = nil
	}

	switch key {
	case termbox.KeyArrowRight:
		if row != nil && gp.cx < len(row.Chars) {
			gp.cx++
		} else if row != nil && gp.cx >= len(row.Chars) {
			gp.cy++
			gp.cx = 0
		}
	case termbox.KeyArrowLeft:
		if gp.cx != 0 {
			gp.cx--
		} else if gp.cy > 0 {
			gp.cy--
			gp.cx = len(gp.doc.Rows[gp.cy].Chars)
		}
	case termbox.KeyArrowDown:
		if gp.cy < gp.doc.Numrows {
			gp.cy++
		}
	case termbox.KeyArrowUp:
		if gp.cy != 0 {
			gp.cy--
		}
	}

	rowlen := 0
	if gp.cy < gp.doc.Numrows {
		rowlen = len(gp.doc.Rows[gp.cy].Chars)
	}
	if rowlen < 0 {
		rowlen = 0
	}
	if gp.cx > rowlen {
		gp.cx = rowlen
	}
}

/*** init ***/

func (gp *gopad) initEditor() {
	gp.screencols, gp.screenrows = termbox.Size()
	gp.screenrows--
	gp.screencols--
}

// func editorRowCxToRx(row *erow, cx int) int {
// 	rx := 0
// 	for j := 0; j < cx; j++ {
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
