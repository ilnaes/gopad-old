package gopad

// Most of these are based on a version of antirez's kilo given by
// https://viewsourcecode.org/snaptoken/kilo/ as well as the editbox demo of termbox-go

import (
	"encoding/json"
	// "fmt"
	"github.com/nsf/termbox-go"
	"log"
	"math/rand"
	// "os"
	// "net/rpc"
	"strconv"
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
	session uint32

	tempRUsers map[int]int // renderX for each tempPos
	numusers   int
}

func StartClient(user int, server string, port int, testing bool) {
	var gp gopad
	gp.id = user
	gp.srv = server + ":" + strconv.Itoa(port)
	gp.tempRUsers = make(map[int]int)
	gp.session = rand.Uint32()

	gp.editorOpen(gp.srv)

	err := termbox.Init()
	if err != nil {
		panic(err)
	}
	defer termbox.Close()
	if !testing {
		termbox.SetInputMode(termbox.InputEsc)
		termbox.SetOutputMode(termbox.Output256)
	}

	gp.initEditor()

	go gp.push()
	go gp.pull(testing)

mainloop:
	for {
		gp.refreshScreen()
		ev := termbox.PollEvent()

		switch ev.Type {
		case termbox.EventKey:
			gp.mu.Lock()
			switch ev.Key {
			case termbox.KeyCtrlC:
				break mainloop
			case termbox.KeyCtrlS:
				gp.mu.Unlock()
				if gp.filename == "" {
					file, ok := gp.editorPrompt("Save as (ESC to cancel): ", gp.filename)
					if ok {
						gp.filename = file
					} else {
						gp.status = ""
						break
					}
				}
				gp.mu.Lock()

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
			case termbox.KeyDelete, termbox.KeyCtrlD:
				gp.logOp([]Op{
					Op{Type: Move, Move: termbox.KeyArrowRight, View: gp.doc.View, Client: gp.id},
					Op{Type: Delete, View: gp.doc.View, Client: gp.id},
				})
			case termbox.KeyTab:
				gp.logOp([]Op{Op{Type: Insert, Data: '\t', View: gp.doc.View, Client: gp.id}})
			case termbox.KeySpace:
				gp.logOp([]Op{Op{Type: Insert, Data: ' ', View: gp.doc.View, Client: gp.id}})
			case termbox.KeyEnter:
				gp.logOp([]Op{Op{Type: Newline, View: gp.doc.View, Client: gp.id}})
			default:
				if ev.Ch != 0 {
					gp.logOp([]Op{Op{Type: Insert, Data: ev.Ch, View: gp.doc.View, Client: gp.id}})
				}
			}
			gp.mu.Unlock()
		case termbox.EventError:
			panic(ev.Err)
		}
	}
}

func (gp *gopad) logOp(ops []Op) {
	for _, op := range ops {
		gp.opNum++
		op.Seq = gp.opNum
		op.Session = gp.session
		gp.selfOps = append(gp.selfOps, op)
		gp.tempdoc.apply(op, true)
	}
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

		done := false
		for !done {
			var reply OpReply
			ok := call(gp.srv, "Server.Handle", OpArg{Data: buf, Xid: rand.Int63()}, &reply, false)

			if ok && reply.Err == "OK" {
				done = true
			}
		}
		time.Sleep(pushDelay)
	}
}

// pulls commited operations from server
func (gp *gopad) pull(testing bool) {
	for {
		var reply QueryReply
		gp.mu.Lock()
		ok := call(gp.srv, "Server.Query", QueryArg{View: gp.doc.View, Client: gp.id}, &reply, false)

		if ok && reply.Err == "OK" {
			var commits []Op
			json.Unmarshal(reply.Data, &commits)

			if len(commits) > 0 {
				// apply commited ops

				oldPoint := gp.doc.UserSeqs[gp.id]
				gp.applyCommits(commits, 0, 0)

				// cut off commiteds
				if gp.doc.UserSeqs[gp.id] > oldPoint {
					gp.selfOps = gp.selfOps[gp.doc.UserSeqs[gp.id]-oldPoint:]
				}

				gp.tempdoc = *gp.doc.dup()
				for k, v := range gp.doc.UserPos {
					x := *v
					gp.tempdoc.UserPos[k] = &x
				}
				// apply ops not yet commited
				for _, op := range gp.selfOps {
					gp.tempdoc.apply(op, true)
				}

				gp.mu.Unlock()
				if !testing {
					gp.refreshScreen()
				}
			} else {
				gp.mu.Unlock()
				time.Sleep(pullDelay)
			}
		} else {
			gp.mu.Unlock()
			time.Sleep(pullDelay)
		}
	}
}

// apply ops and returns true if a certain Init op was found
func (gp *gopad) applyCommits(commits []Op, session uint32, ck int) bool {
	res := false

	for _, op := range commits {
		// gp.status = fmt.Sprintf("%d %v", gp.doc.Seqs[op.Client], commits)
		if gp.doc.apply(op, false) {
			// apply op and update commitpoint

			if op.Type == Init {
				if gp.doc.UserSeqs[op.Client] == 1 {
					gp.numusers++
				}

				if op.Session == session && op.Client == ck {
					res = true
				}
			}
		}
	}

	return res
}

/*** file i/o ***/

// get file from server
func (gp *gopad) editorOpen(server string) {
	var reply InitReply
	for {
		ok := call(server, "Server.Init", InitArg{Client: gp.id, Session: gp.session}, &reply, false)
		if ok {
			if reply.Err == "OK" {
				err := bytesToDoc(reply.Doc, &gp.doc)
				if err != nil {
					log.Fatal("Couldn't decode document")
				}

				// process updates until relevant Init
				done := false
				for !done {
					var reply QueryReply
					ok := call(gp.srv, "Server.Query", QueryArg{View: gp.doc.View, Client: gp.id}, &reply, false)

					if ok && reply.Err == "OK" {
						// apply commited ops
						var commits []Op
						json.Unmarshal(reply.Data, &commits)

						if len(commits) > 0 {
							done = gp.applyCommits(commits, gp.session, gp.id)
						}
					} else {
						time.Sleep(pullDelay)
					}
				}

				gp.tempdoc = *gp.doc.dup()
				gp.opNum = gp.doc.UserSeqs[gp.id]
				gp.numusers = len(gp.doc.UserPos)

				// update positions
				for user, pos := range gp.doc.UserPos {
					gp.tempRUsers[user] = editorRowCxToRx(&gp.tempdoc.Rows[pos.Y], pos.X)
				}
				break
			} else {
				time.Sleep(time.Second)
			}

		} else {
			log.Fatal("Not ok call")
		}
	}
}

func (gp *gopad) editorPrompt(msg, file string) (string, bool) {
	gp.status = msg

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
		if pos.Y < len(gp.tempdoc.Rows) {
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
								if gp.tempRUsers[user]-gp.coloff == k && pos.Y == filerow {
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
	// gp.status = fmt.Sprintf("%v", gp.tempRUsers)
	gp.mu.Lock()
	const coldef = termbox.ColorDefault
	termbox.Clear(coldef, coldef)
	gp.initEditor()

	gp.editorScroll()
	gp.drawRows()
	gp.editorDrawStatusBar()

	termbox.SetCursor(gp.tempRUsers[gp.id]-gp.coloff+1, gp.tempdoc.UserPos[gp.id].Y-gp.rowoff)
	termbox.Flush()
	gp.mu.Unlock()
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
