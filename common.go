package main

import (
	// "encoding/binary"
	"github.com/nsf/termbox-go"
	"log"
	"net/rpc"
)

var COLORS = []termbox.Attribute{16, 10, 11, 15, 0}
var CURSORS = []termbox.Attribute{253, 211, 121, 124, 0}

const MAXUSERS = 3
const TABSTOP = 4

const (
	Port = ":6060"
)

const (
	Insert = iota
	Delete
	Newline
	Init
	Move
)

type Err string

type erow struct {
	Chars  string
	Temp   []bool // really shouldn't be here but whatever
	Author []uint32
}

type Pos struct {
	X, Y int
}

type Doc struct {
	Rows  []erow
	View  uint32
	Users map[uint32]*Pos // position of users in document

	// optional really
	Numrows int
	Id      uint32
}

type Op struct {
	Type int
	Data rune
	Move termbox.Key
	// X, Y   int
	View   uint32 // last document view seen by user
	ID     uint32 // sequential number for each user
	Client uint32
}

type InitArg struct {
	Client uint32
	View   uint32
}

type QueryArg struct {
	View uint32
}

type OpArg struct {
	Data []byte
}

type InitReply struct {
	Doc     []byte
	Commits []byte
	Err     Err
}

type OpReply struct {
	Err Err
}

type QueryReply struct {
	Data []byte
	Err  Err
}

func call(srv string, rpcname string, args interface{}, reply interface{}, verbose bool) bool {

	// attempt to dial
	c, err := rpc.Dial("tcp", srv)
	if err != nil {
		if verbose {
			log.Println("Couldn't connect to", srv)
		}
		return false
	}
	defer c.Close()

	err = c.Call(rpcname, args, reply)
	if err != nil {
		if verbose {
			log.Println(err)
		}
		return false
	}
	return true
}

/*** copy functions ***/

func (row *erow) copy() *erow {
	t := make([]bool, len(row.Temp))
	copy(t, row.Temp)
	a := make([]uint32, len(row.Author))
	copy(a, row.Author)

	return &erow{
		Chars:  row.Chars,
		Temp:   t,
		Author: a,
	}
}

func (doc *Doc) copy() *Doc {
	d := Doc{View: doc.View, Numrows: doc.Numrows}
	d.Rows = make([]erow, len(doc.Rows))
	for i := 0; i < len(d.Rows); i++ {
		d.Rows[i] = *doc.Rows[i].copy()
	}
	d.Users = make(map[uint32]*Pos)

	for k, v := range doc.Users {
		pos := *v
		d.Users[k] = &pos
	}
	return &d
}

/*** input ***/

func editorMoveCursor(doc *Doc, id uint32, key termbox.Key) {
	pos := doc.Users[id]
	var row *erow
	if pos.Y < doc.Numrows {
		row = &doc.Rows[pos.Y]
	} else {
		row = nil
	}

	oldRx := editorRowCxToRx(&doc.Rows[pos.Y], pos.X)

	switch key {
	case termbox.KeyArrowRight:
		if row != nil && pos.X < len(row.Chars) {
			pos.X++
		} else if row != nil && pos.X >= len(row.Chars) {
			pos.Y++
			pos.X = 0
		}
		return
	case termbox.KeyArrowLeft:
		if pos.X != 0 {
			pos.X--
		} else if pos.Y > 0 {
			pos.Y--
			pos.X = len(doc.Rows[pos.Y].Chars)
		}
		return
	case termbox.KeyArrowDown:
		if pos.Y < doc.Numrows-1 {
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
		rowlen = editorRowCxToRx(&doc.Rows[pos.Y], len(doc.Rows[pos.Y].Chars))
	}
	if rowlen < 0 {
		rowlen = 0
	}
	if oldRx > rowlen {
		pos.X = len(doc.Rows[pos.Y].Chars)
	} else {
		pos.X = editorRowRxToCx(&doc.Rows[pos.Y], oldRx)
	}
}

/*** editor operations ***/

func editorInsertRune(doc *Doc, id uint32, key rune, temp bool) {
	pos := doc.Users[id]

	if pos.Y == doc.Numrows {
		doc.insertRow(pos.Y, "", []bool{}, []uint32{})
	}

	doc.rowInsertRune(pos.X, pos.Y, key, id, temp)

	for k, npos := range doc.Users {
		// update other positions
		if k != id {
			if pos.Y == npos.Y && pos.X <= npos.X {
				npos.X++
			}
		}
	}

	pos.X++
}

func editorInsertNewLine(doc *Doc, id uint32) {
	pos := doc.Users[id]

	if pos.X == 0 {
		doc.insertRow(pos.Y, "", []bool{}, []uint32{})
	} else {
		row := &doc.Rows[pos.Y]
		doc.insertRow(pos.Y+1, row.Chars[pos.X:], row.Temp[pos.X:], row.Author[pos.X:])
		doc.Rows[pos.Y].Chars = row.Chars[:pos.X]
		doc.Rows[pos.Y].Temp = row.Temp[:pos.X]
		doc.Rows[pos.Y].Author = row.Author[:pos.X]
	}

	for k, npos := range doc.Users {
		// update other positions
		if k != id {
			if pos.Y == npos.Y && pos.X <= npos.X {
				npos.Y++
				npos.X -= pos.X
			} else if pos.Y < npos.Y {
				npos.Y++
			}
		}
	}

	pos.Y++
	pos.X = 0
}

func editorDelRune(doc *Doc, id uint32) {
	pos := doc.Users[id]
	if pos.Y == doc.Numrows {
		return
	}
	if pos.X == 0 && pos.Y == 0 {
		return
	}

	if pos.X > 0 {
		doc.rowDelRune(pos.X, pos.Y)

		for k, npos := range doc.Users {
			// update other positions
			if k != id {
				if pos.Y == npos.Y && pos.X <= npos.X {
					npos.X--
				}
			}
		}
		pos.X--
	} else {
		oldOffset := len(doc.Rows[pos.Y-1].Chars)
		doc.editorDelRow(pos.Y)

		for k, npos := range doc.Users {
			// update other positions
			if k != id {
				if pos.Y == npos.Y {
					npos.X += oldOffset
					npos.Y--

				} else if pos.Y < npos.Y {
					npos.Y--
				}
			}
		}

		pos.X = oldOffset
		pos.Y--
	}
}

/*** row operations ***/

func (doc *Doc) insertRow(at int, ch string, temp []bool, auth []uint32) {
	// doc.Rows = append(doc.Rows, erow{Chars: s})
	doc.Rows = append(doc.Rows, erow{})
	copy(doc.Rows[at+1:], doc.Rows[at:])
	doc.Rows[at] = erow{Chars: ch, Temp: temp, Author: auth}
	doc.Numrows++
}

// insert rune into row[aty] at position atx
func (doc *Doc) rowInsertRune(atx, aty int, key rune, id uint32, temp bool) {
	row := &doc.Rows[aty]

	if atx < 0 || atx > len(row.Chars) {
		atx = len(row.Chars)
	}

	s := row.Chars[0:atx]
	s += string(key)
	s += row.Chars[atx:]
	row.Chars = s

	row.Temp = append(row.Temp, false)
	copy(row.Temp[atx+1:], row.Temp[atx:])
	row.Temp[atx] = temp

	row.Author = append(row.Author, 0)
	copy(row.Author[atx+1:], row.Author[atx:])
	row.Author[atx] = id
}

// delete a rune
func (doc *Doc) rowDelRune(atx, aty int) {
	row := &doc.Rows[aty]

	if atx < 0 || atx > len(row.Chars) {
		atx = len(row.Chars)
	}

	row.Chars = row.Chars[0:atx-1] + row.Chars[atx:]
	row.Temp = append(row.Temp[0:atx-1], row.Temp[atx:]...)
	row.Author = append(row.Author[0:atx-1], row.Author[atx:]...)
}

func (doc *Doc) editorDelRow(at int) {
	if at < 0 || at >= doc.Numrows {
		return
	}

	doc.Rows[at-1].Chars += doc.Rows[at].Chars
	doc.Rows[at-1].Temp = append(doc.Rows[at-1].Temp, doc.Rows[at].Temp...)
	doc.Rows[at-1].Author = append(doc.Rows[at-1].Author, doc.Rows[at].Author...)
	copy(doc.Rows[at:], doc.Rows[at+1:])
	doc.Rows = doc.Rows[:len(doc.Rows)-1]
	doc.Numrows--
}

/*** tabs ***/

func editorRowCxToRx(row *erow, atx int) int {
	rx := 0
	for j := 0; j < atx; j++ {
		if row.Chars[j] == '\t' {
			rx += (TABSTOP - 1) - (rx % TABSTOP)
		}
		rx++
	}
	return rx
}

func editorRowRxToCx(row *erow, rx int) int {
	cur_rx := 0
	cx := 0
	for ; cx < len(row.Chars); cx++ {
		if row.Chars[cx] == '\t' {
			cur_rx += (TABSTOP - 1) - (cur_rx % TABSTOP)
		}
		cur_rx++

		if cur_rx > rx {
			return cx
		}
	}
	return cx
}
