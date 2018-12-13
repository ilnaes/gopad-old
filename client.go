package main

import (
	"bufio"
	"encoding/gob"
	"github.com/nsf/termbox-go"
	"io"
	"log"
	"net"
	// "sync"
)

/*** row operations ***/

func (gp *gopad) editorInsertRow(at int, s string) {
	// gp.rows = append(gp.rows, erow{Chars: s})
	gp.rows = append(gp.rows, erow{})
	copy(gp.rows[at+1:], gp.rows[at:])
	gp.rows[at] = erow{Chars: s}
	// gp.rows[gp.numrows].render = renderRow(s)
	gp.numrows++
}

func (gp *gopad) rowInsertRune(key rune) {
	row := &gp.rows[gp.cy]

	at := gp.cx
	if at < 0 || at > len(row.Chars) {
		at = len(row.Chars)
	}

	s := row.Chars[0:gp.cx]
	s += string(key)
	s += row.Chars[gp.cx:]
	row.Chars = s
}

func (gp *gopad) rowDelRune() {
	row := &gp.rows[gp.cy]

	at := gp.cx
	if at < 0 || at > len(row.Chars) {
		at = len(row.Chars)
	}

	s := row.Chars[0:gp.cx-1] + row.Chars[gp.cx:]
	row.Chars = s
}

func (gp *gopad) editorDelRow() {
	if gp.cy < 0 || gp.cy >= gp.numrows {
		return
	}

	gp.rows[gp.cy-1].Chars += gp.rows[gp.cy].Chars
	copy(gp.rows[gp.cy:], gp.rows[gp.cy+1:])
	gp.rows = gp.rows[:len(gp.rows)-1]
	gp.numrows--
}

/*** editor operations ***/

func (gp *gopad) editorInsertRune(key rune) {
	if gp.cy == gp.numrows {
		gp.editorInsertRow(gp.cy, "")
	}

	gp.rowInsertRune(key)
	gp.cx++
}

func (gp *gopad) editorInsertNewLine() {
	if gp.cx == 0 {
		gp.editorInsertRow(gp.cy, "")
	} else {
		row := &gp.rows[gp.cy]
		gp.editorInsertRow(gp.cy+1, row.Chars[gp.cx:])
		row.Chars = row.Chars[:gp.cx]
	}
	gp.cy++
	gp.cx = 0
}

func (gp *gopad) editorDelRune() {
	if gp.cy == gp.numrows {
		return
	}
	if gp.cx == 0 && gp.cy == 0 {
		return
	}

	if gp.cx > 0 {
		gp.rowDelRune()
		gp.cx--
	} else {
		gp.cx = len(gp.rows[gp.cy-1].Chars)
		gp.editorDelRow()
		gp.cy--
	}
}

/*** file i/o ***/

func (gp *gopad) editorOpen() {
	// file, err := os.Open("test")
	// if err != nil {
	// 	log.Fatal(err)
	// }
	// defer file.Close()
	var d Doc
	dec := gob.NewDecoder(gp.buf)
	err := dec.Decode(&d)
	if err != nil {
		termbox.Close()
		log.Fatal("Couldn't decode document")
	}

	// scanner := bufio.NewScanner(file)
	for _, row := range d.Rows {
		gp.editorInsertRow(gp.numrows, row.Chars)
	}
}

/*** output ***/

func (gp *gopad) editorScroll() {
	// gp.rx = 0
	// if gp.cy < gp.numrows {
	// 	gp.rx = editorRowCxToRx(&gp.rows[gp.cy], gp.cx)
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
		if filerow < gp.numrows {
			termbox.SetCell(0, i, '~', coldef, coldef)
			x := 0
			for k, s := range gp.rows[filerow].Chars {
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
	if gp.cy < gp.numrows {
		row = &gp.rows[gp.cy]
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
			gp.cx = len(gp.rows[gp.cy].Chars)
		}
	case termbox.KeyArrowDown:
		if gp.cy < gp.numrows {
			gp.cy++
		}
	case termbox.KeyArrowUp:
		if gp.cy != 0 {
			gp.cy--
		}
	}

	rowlen := 0
	if gp.cy < gp.numrows {
		rowlen = len(gp.rows[gp.cy].Chars)
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

	conn, err := net.Dial("tcp", server+Port)
	if err != nil {
		log.Fatal("Couldn't dial")
	}
	defer conn.Close()
	gp.buf = bufio.NewReadWriter(bufio.NewReader(conn), bufio.NewWriter(conn))

	gp.initEditor()
	gp.editorOpen()

	go gp.poll()

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
				if gp.cy < gp.numrows {
					gp.cx = len(gp.rows[gp.cy].Chars)
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
					gp.buf.WriteString(string(ev.Ch) + "\n")
					gp.buf.Flush()
				}
			}
		case termbox.EventError:
			panic(ev.Err)
		}
		gp.refreshScreen()
	}
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
