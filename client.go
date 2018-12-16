package main

// Most of these are based on a stripped down version of
// the text editor of https://viewsourcecode.org/snaptoken/kilo/ which
// is based on antirez's kilo

import (
	"bufio"
	"bytes"
	"encoding/gob"
	"github.com/nsf/termbox-go"
	"io"
	"log"
	"net/rpc"
	"sync"
)

type gopad struct {
	screenrows int
	screencols int
	rowoff     int
	coloff     int
	// rx         int
	cx  int
	cy  int
	doc Doc
	buf *bufio.ReadWriter
	mu  sync.Mutex
}

/*** editor operations ***/

func (gp *gopad) editorInsertRune(key rune) {
	if gp.cy == gp.doc.numrows {
		gp.doc.insertRow(gp.cy, "")
	}

	gp.doc.rowInsertRune(gp.cx, gp.cy, key)
	gp.cx++
}

func (gp *gopad) editorInsertNewLine() {
	if gp.cx == 0 {
		gp.doc.insertRow(gp.cy, "")
	} else {
		row := &gp.doc.Rows[gp.cy]
		gp.doc.insertRow(gp.cy+1, row.Chars[gp.cx:])
		row.Chars = row.Chars[:gp.cx]
	}
	gp.cy++
	gp.cx = 0
}

func (gp *gopad) editorDelRune() {
	if gp.cy == gp.doc.numrows {
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

/*** file i/o ***/

func (gp *gopad) editorOpen(server string) {
	// file, err := os.Open("test")
	// if err != nil {
	// 	log.Fatal(err)
	// }
	// defer file.Close()
	var d Doc
	var reply Reply
	ok := call(server, "Server.Init", Args{}, &reply)
	if ok {
		buf := bytes.NewBuffer(reply.Data)
		dec := gob.NewDecoder(buf)
		err := dec.Decode(&d)

		if err != nil {
			termbox.Close()
			log.Fatal("Couldn't decode document")
		}

		// scanner := bufio.NewScanner(file)
		for _, row := range d.Rows {
			gp.doc.insertRow(gp.doc.numrows, row.Chars)
		}
	} else {
		log.Println("Not ok call")
	}
}

/*** output ***/

func (gp *gopad) editorScroll() {
	// gp.rx = 0
	// if gp.cy < gp.doc.numrows {
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

func (gp *gopad) drawRows() {
	const coldef = termbox.ColorDefault

	i := 0
	for ; i < gp.screenrows; i++ {
		filerow := i + gp.rowoff
		if filerow < gp.doc.numrows {
			termbox.SetCell(0, i, '~', coldef, coldef)
			x := 0
			for k, s := range gp.doc.Rows[filerow].Chars {
				if k >= gp.coloff {
					termbox.SetCell(x+1, i, s, coldef, coldef)
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
	if gp.cy < gp.doc.numrows {
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
		if gp.cy < gp.doc.numrows {
			gp.cy++
		}
	case termbox.KeyArrowUp:
		if gp.cy != 0 {
			gp.cy--
		}
	}

	rowlen := 0
	if gp.cy < gp.doc.numrows {
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

func StartClient(server string) {
	err := termbox.Init()
	if err != nil {
		panic(err)
	}
	defer termbox.Close()
	termbox.SetInputMode(termbox.InputEsc)

	var gp gopad

	// conn, err := net.Dial("tcp", server+Port)
	// if err != nil {
	// 	termbox.Close()
	// 	log.Fatal("Couldn't dial")
	// }
	// defer conn.Close()
	// gp.buf = bufio.NewReadWriter(bufio.NewReader(conn), bufio.NewWriter(conn))

	gp.initEditor()
	gp.editorOpen(server + ":6060")

	// go gp.poll()

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
				if gp.cy < gp.doc.numrows {
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
				gp.editorInsertRune(' ')
			case termbox.KeyEnter:
				gp.editorInsertNewLine()
			default:
				if ev.Ch != 0 {
					// gp.editorInsertRune(ev.Ch)
					// gp.buf.WriteString(string(ev.Ch) + "\n")
					// gp.buf.Flush()
					var reply Reply
					arg := Args{Op: "Insert"}
					arg.Data = []byte(string(ev.Ch))
					ok := call(server+":6060", "Server.Commit", arg, &reply)
					if !ok {
						log.Fatal("Couldn't commit!")
					}
					gp.editorInsertRune(ev.Ch)
				}
			}
		case termbox.EventError:
			panic(ev.Err)
		}
		gp.refreshScreen()
	}
}

// rpc caller
func call(srv string, rpcname string,
	args interface{}, reply interface{}) bool {
	c, errx := rpc.DialHTTP("tcp", srv)
	if errx != nil {
		log.Println("Couldn't connect to", srv)
		return false
	}
	defer c.Close()

	err := c.Call(rpcname, args, reply)
	if err != nil {
		log.Println("Couldn't call", err)
		return false
	}

	return true
}

func (gp *gopad) poll() {
	for {
		r, _, err := gp.buf.ReadRune()
		switch {
		case err == io.EOF:
			return
		case err != nil:
			return
		}

		// cmd = strings.Trim(cmd, "\n ")
		gp.editorInsertRune(r)
		gp.refreshScreen()
	}
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
