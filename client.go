package main

// Most of these are based on a version of antirez's kilo given by
// https://viewsourcecode.org/snaptoken/kilo/ as well as the editbox demo of termbox-go

import (
	"encoding/json"
	"fmt"
	"github.com/nsf/termbox-go"
	"log"
	"math/rand"
	// "os"
	// "net/rpc"
	"strings"
	"sync"
	"time"
)

const (
	pushDelay = 250 * time.Millisecond
	pullDelay = 250 * time.Millisecond
	// pushDelay = 1 * time.Second
	// pullDelay = 1 * time.Second
)

type gopad struct {
	filename   string
	srv        string
	id         int
	screenrows int
	screencols int
	rowoff     int
	coloff     int
	doc        Doc
	tempdoc    Doc
	mu         sync.Mutex
	status     string

	selfOps []Op
	opNum   uint32

	tempRUsers map[int]int // renderX for each tempPos
	numusers   int
}

func StartClient(user int, server string) {
	var gp gopad
	gp.id = user
	gp.srv = server + Port
	gp.tempRUsers = make(map[int]int)

	fmt.Println(server + Port)

	gp.editorOpen(server+Port, 0, user)
	gp.status = fmt.Sprintf("%d", gp.doc.View)

	// 	if len(gp.doc.Users) > 1 {
	// 		f, err := os.Create("tmp1")
	// 		if err != nil {
	// 			log.Fatal(err)
	// 		}

	// 		for _, r := range gp.doc.Rows {
	// 			f.WriteString(r.Chars + string('\n'))
	// 		}
	// 		f.Sync()
	// 		f.Close()
	// 		os.Rename("tmp1", "tmp")

	// 	}

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
			case termbox.KeyCtrlS:
				if gp.filename == "" {
					file, ok := gp.editorPrompt("Save as (ESC to cancel): ")
					if ok {
						gp.filename = file
					} else {
						gp.status = ""
						break
					}
				}

				if gp.tempdoc.write(gp.filename) {
					gp.status = "Saved!"
				}
			case termbox.KeyArrowLeft,
				termbox.KeyArrowRight,
				termbox.KeyArrowUp,
				termbox.KeyArrowDown,
				termbox.KeyHome,
				termbox.KeyEnd:
				gp.logOp([]Op{Op{Type: Move, Move: ev.Key, View: gp.doc.View, Client: gp.id}})
				editorMoveCursor(&gp.tempdoc, gp.id, ev.Key)
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
				editorDelRune(&gp.tempdoc, gp.id)
			case termbox.KeyDelete, termbox.KeyCtrlD:
				gp.logOp([]Op{
					Op{Type: Move, Move: termbox.KeyArrowRight, View: gp.doc.View, Client: gp.id},
					Op{Type: Delete, View: gp.doc.View, Client: gp.id},
				})
				editorMoveCursor(&gp.tempdoc, gp.id, termbox.KeyArrowRight)
				editorDelRune(&gp.tempdoc, gp.id)
			case termbox.KeyTab:
				gp.logOp([]Op{Op{Type: Insert, Data: '\t', View: gp.doc.View, Client: gp.id}})
				editorInsertRune(&gp.tempdoc, gp.id, '\t', true)
			case termbox.KeySpace:
				gp.logOp([]Op{Op{Type: Insert, Data: ' ', View: gp.doc.View, Client: gp.id}})
				editorInsertRune(&gp.tempdoc, gp.id, ' ', true)
			case termbox.KeyEnter:
				gp.logOp([]Op{Op{Type: Newline, View: gp.doc.View, Client: gp.id}})
				editorInsertNewLine(&gp.tempdoc, gp.id)
			default:
				if ev.Ch != 0 {
					gp.logOp([]Op{Op{Type: Insert, Data: ev.Ch, View: gp.doc.View, Client: gp.id}})
					editorInsertRune(&gp.tempdoc, gp.id, ev.Ch, true)
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
		op.Seq = gp.opNum
		gp.selfOps = append(gp.selfOps, op)
	}
	gp.mu.Unlock()
}

// push commits to server
func (gp *gopad) push() {
	for {
		gp.mu.Lock()
		if len(gp.selfOps) == 0 {
			// no new ops
			gp.mu.Unlock()
			time.Sleep(pushDelay)
			continue
		}
		// prepare and send new ops
		buf, err := json.Marshal(gp.selfOps)
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
				// gp.mu.Lock()
				// if reply.Err == "OK" {
				// 	// update op sent point
				// 	gp.sentpoint = l
				// }
				// gp.mu.Unlock()
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
			ok = call(gp.srv, "Server.Query", QueryArg{View: gp.doc.View, Client: gp.id}, &reply, false)

			if ok && reply.Err == "OK" {
				var commits []Op
				json.Unmarshal(reply.Data, &commits)

				if len(commits) > 0 {
					// apply commited ops
					gp.mu.Lock()

					oldPoint := gp.doc.Seqs[gp.id]
					gp.applyCommits(commits)

					// cut off commiteds
					if gp.doc.Seqs[gp.id] > oldPoint {
						gp.selfOps = gp.selfOps[gp.doc.Seqs[gp.id]-oldPoint:]
					}

					gp.tempdoc = *gp.doc.copy()
					for k, v := range gp.doc.UserPos {
						x := *v
						gp.tempdoc.UserPos[k] = &x
					}
					// apply ops not yet commited
					for _, op := range gp.selfOps {
						gp.tempdoc.apply(op, true)
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

// apply ops from commit log
func (gp *gopad) applyCommits(commits []Op) {
	for _, op := range commits {
		// gp.status = fmt.Sprintf("%d %v", gp.doc.Seqs[op.Client], commits)
		if op.Seq == gp.doc.Seqs[op.Client]+1 {
			// apply op and update commitpoint
			gp.doc.apply(op, false)
			gp.doc.Seqs[op.Client]++

			if op.Type == Init && gp.doc.Seqs[op.Client] == 0 {
				gp.numusers++
			}
		}
		gp.doc.View++
	}
}

func (gp *gopad) editorPrompt(msg string) (string, bool) {
	gp.status = msg
	file := ""

	for {
		gp.refreshScreen()

		switch ev := termbox.PollEvent(); ev.Type {
		case termbox.EventKey:
			switch ev.Key {
			case termbox.KeyBackspace, termbox.KeyBackspace2:
				file = file[:len(file)-1]
				gp.status = msg + file
			case termbox.KeyEsc:
				return "", false
			case termbox.KeyEnter:
				return file, true
			default:
				if ev.Ch != 0 {
					file += string(ev.Ch)
					gp.status = msg + file
				}
			}
		case termbox.EventError:
			panic(ev.Err)
		}
	}
}

/*** output ***/

func (gp *gopad) editorScroll() {

	for id, pos := range gp.tempdoc.UserPos {
		gp.tempRUsers[id] = 0
		if pos.Y < len(gp.doc.Rows) {
			gp.tempRUsers[id] = editorRowCxToRx(&gp.tempdoc.Rows[pos.Y], pos.X)
		}
	}

	pos := gp.tempdoc.UserPos[gp.id]
	// reposition up

	if pos.Y < gp.rowoff {
		gp.rowoff = pos.Y
	}

	// reposition down
	if pos.Y >= gp.rowoff+gp.screenrows {
		gp.rowoff = pos.Y - gp.screenrows + 1
	}

	if gp.tempRUsers[gp.id] < gp.coloff {
		gp.coloff = gp.tempRUsers[gp.id]
	}

	if gp.tempRUsers[gp.id] >= gp.coloff+gp.screencols {
		gp.coloff = gp.tempRUsers[gp.id] - gp.screencols + 1
	}
}

// draw a row
func (gp *gopad) drawRows() {
	const coldef = termbox.ColorDefault

	i := 0
	for ; i < gp.screenrows; i++ {
		filerow := i + gp.rowoff
		if filerow < len(gp.tempdoc.Rows) {

			row := gp.tempdoc.Rows[filerow].renderRow()

			// draw gutters
			termbox.SetCell(0, i, '~', coldef, coldef)

			if len(row.Chars) > 0 {
				for k, s := range row.Chars {
					if k >= gp.coloff {
						bg := termbox.ColorDefault

						// draw other cursors
						for user, pos := range gp.tempdoc.UserPos {
							if user != gp.id {
								if gp.tempRUsers[user] == k && pos.Y == filerow {
									bg = CURSORS[gp.doc.Colors[user]]
								}
							}
						}

						if row.Temp[k] {
							// is temp char?
							termbox.SetCell(k+1-gp.coloff, i, s, 251, bg)
						} else {

							// select color based on author
							auth := row.Author[k]
							color := COLORS[auth]
							termbox.SetCell(k+1-gp.coloff, i, s, color, bg)
						}
						if k+1 > gp.screencols {
							break
						}
					}
				}

				end := len(gp.tempdoc.Rows[filerow].Chars)
				endR := len(row.Chars)

				// at endpoint?
				if endR >= gp.coloff && endR < gp.coloff+gp.screencols {
					for user, pos := range gp.tempdoc.UserPos {
						if user != gp.id {
							if pos.X == end && pos.Y == filerow {
								termbox.SetCell(endR-gp.coloff+1, i, ' ', 0, CURSORS[gp.doc.Colors[user]])
							}
						}
					}
				}
			} else if gp.coloff == 0 {
				// draw cursor on empty line
				for user, pos := range gp.tempdoc.UserPos {
					if user != gp.id {
						if pos.Y == filerow {
							termbox.SetCell(1, i, ' ', 0, CURSORS[gp.doc.Colors[user]])
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

	if gp.doc.Colors[gp.id] != 0 {
		bg = COLORS[gp.doc.Colors[gp.id]]
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

	termbox.SetCursor(gp.tempRUsers[gp.id]-gp.coloff+1, gp.tempdoc.UserPos[gp.id].Y-gp.rowoff)
	termbox.Flush()
	gp.mu.Unlock()
}

/*** file i/o ***/

// get file from server
func (gp *gopad) editorOpen(server string, view int, user int) {
	var reply InitReply
	xid := rand.Uint32()
	for {
		ok := call(server, "Server.Init", InitArg{Client: user, Xid: xid}, &reply, false)
		if ok {
			if reply.Err == "OK" {
				// TODO: update for reconnection
				err := bytesToDoc(reply.Doc, &gp.doc)
				if err != nil {
					log.Fatal("Couldn't decode document")
				}

				gp.tempdoc = *gp.doc.copy()
				gp.opNum = gp.doc.Seqs[user]

				// // update positions
				for user, pos := range gp.doc.UserPos {
					gp.tempRUsers[user] = editorRowCxToRx(&gp.tempdoc.Rows[pos.Y], pos.X)
				}
				gp.numusers = len(gp.doc.UserPos)
				break
			} else {
				time.Sleep(time.Second)
			}

			// var c []Op
			// err = json.Unmarshal(reply.Commits, &c)
			// if err != nil {
			// 	log.Fatal("Couldn't decode document")
			// }

			// gp.opNum = 1

			// if len(c) > 0 {
			// 	gp.applyCommits(c)
			// }
		} else {
			log.Fatal("Not ok call")
		}
	}
}

/*** init ***/

func (gp *gopad) initEditor() {
	gp.screencols, gp.screenrows = termbox.Size()
	gp.screenrows--
	gp.screencols--
}

func (row *erow) renderRow() *erow {
	newrow := erow{}

	tabs := 0
	for i := 0; i < len(row.Chars); i++ {
		if row.Chars[i] == '\t' {
			tabs++
		}
	}
	l := len(row.Chars) + tabs*(TABSTOP-1) + 1

	newrow.Temp = make([]bool, l)
	newrow.Author = make([]int, l)

	var sb strings.Builder
	x := 0
	for i, r := range row.Chars {
		if r == '\t' {
			for {
				sb.WriteRune(' ')
				x++
				if x%TABSTOP == 0 {
					break
				}
			}
		} else {
			sb.WriteRune(r)
			newrow.Temp[x] = row.Temp[i]
			newrow.Author[x] = row.Author[i]
			x++
		}
	}
	newrow.Chars = sb.String()
	return &newrow
}
