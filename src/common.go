package gopad

import (
	"bytes"
	"encoding/gob"
	"github.com/nsf/termbox-go"
	"log"
	"net/rpc"
	"os"
)

var COLORS = []termbox.Attribute{16, 10, 11, 15, 0}
var CURSORS = []termbox.Attribute{253, 211, 121, 124, 0}

const MAXUSERS = 3
const TABSTOP = 4

const (
	Port = 6060
)

const (
	Insert = iota
	Delete
	Newline
	Init
	Move
	Quit
)

type Err string

type erow struct {
	Chars  string
	Temp   []bool // really shouldn't be here but whatever
	Author []int
}

type Pos struct {
	X, Y int
}

type Doc struct {
	Rows        []erow
	View        uint32
	Colors      map[int]int
	Users       []string    // starting username (not implemented)
	UserPos     map[int]Pos // position of users in document
	UserSeqs    map[int]uint32
	UserSession map[int]uint32
}

// transport version of doc
// type Doc_t struct {
// 	Users       []string
// 	Rows        []erow
// 	View        uint32
// 	Colors      map[int]int
// 	UserPos     map[int]Pos // position of users in document
// 	UserSeqs    map[int]uint32
// 	UserSession map[int]uint32
// }

type Op struct {
	Type int
	Data rune
	Move termbox.Key
	// X, Y   int
	View    uint32 // last document view seen by user
	Seq     uint32 // sequential number for each user
	Client  int
	Session uint32
}

type InitArg struct {
	Client  int
	Session uint32
}

type QueryArg struct {
	View   uint32
	Client int
}

type OpArg struct {
	Data []byte
	Xid  int64
}

type RecoverArg struct {
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

type RecoverReply struct {
	Srv []byte
	Px  []byte
	Doc []byte
	Err Err
}

func call(srv string, rpcname string, args interface{}, reply interface{}, verbose bool) bool {

	// attempt to dial
	c, err := rpc.Dial("tcp", srv)
	if err != nil {
		if verbose {
			log.Printf("Couldn't connect to %s -- %s\n", srv, rpcname)
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
	a := make([]int, len(row.Author))
	copy(a, row.Author)

	return &erow{
		Chars:  row.Chars,
		Temp:   t,
		Author: a,
	}
}

// write doc
func (doc *Doc) write(filename string) bool {
	f, err := os.OpenFile(filename, os.O_RDWR|os.O_CREATE, 0666)

	if err != nil {
		return false
	}

	for _, row := range doc.Rows {
		f.WriteString(row.Chars + string("\n"))
	}
	f.Close()
	return true
}

// copies doc
func (doc *Doc) dup() *Doc {
	d := Doc{View: doc.View}
	d.Rows = make([]erow, len(doc.Rows))
	for i := 0; i < len(d.Rows); i++ {
		d.Rows[i] = *doc.Rows[i].copy()
	}

	d.UserSeqs = make(map[int]uint32)
	d.Users = make([]string, len(doc.Users))
	d.Colors = make(map[int]int)
	d.UserSession = make(map[int]uint32)

	copy(d.Users, doc.Users)

	for k, v := range doc.Colors {
		d.Colors[k] = v
	}

	for k, v := range doc.UserSeqs {
		d.UserSeqs[k] = v
	}

	for k, v := range doc.UserSession {
		d.UserSession[k] = v
	}

	d.UserPos = make(map[int]Pos)
	for k, v := range doc.UserPos {
		d.UserPos[k] = v
	}
	return &d
}

// convert doc to byte array
func docToBytes(doc *Doc) ([]byte, error) {
	// d := Doc_t{View: doc.View}
	// d.Rows = make([]erow, len(doc.Rows))
	// for i := 0; i < len(d.Rows); i++ {
	// 	d.Rows[i] = *doc.Rows[i].copy()
	// }

	// // need to dereference map targets to send
	// d.UserPos = make(map[int]Pos)
	// for k, v := range doc.UserPos {
	// 	d.UserPos[k] = v
	// }
	// d.UserSeqs = make(map[int]uint32)
	// d.Users = make([]string, len(doc.Users))
	// d.Colors = make(map[int]int)
	// d.UserSession = make(map[int]uint32)

	// copy(d.Users, doc.Users)

	// for k, v := range doc.Colors {
	// 	d.Colors[k] = v
	// }

	// for k, v := range doc.UserSeqs {
	// 	d.UserSeqs[k] = v
	// }

	// for k, v := range doc.UserSession {
	// 	d.UserSession[k] = v
	// }

	var b bytes.Buffer

	enc := gob.NewEncoder(&b)
	err := enc.Encode(doc)

	if err != nil {
		panic(err)
	}

	return b.Bytes(), err
}

// convert byte array back to doc
func bytesToDoc(b []byte, d *Doc) error {
	buf := bytes.NewBuffer(b)

	dec := gob.NewDecoder(buf)
	err := dec.Decode(d)
	if err != nil {
		log.Fatal("decode:", err)
	}

	// d.View = doc.View

	// d.Rows = make([]erow, len(doc.Rows))
	// for i := 0; i < len(d.Rows); i++ {
	// 	d.Rows[i] = *doc.Rows[i].copy()
	// }

	// d.UserSeqs = doc.UserSeqs
	// d.Users = doc.Users
	// d.UserSession = doc.UserSession

	// d.UserPos = make(map[int]Pos)
	// d.Colors = make(map[int]int)

	// for k, v := range doc.Colors {
	// 	d.Colors[k] = v
	// }

	// for k, v := range doc.UserPos {
	// 	d.UserPos[k] = v
	// }

	return err
}

// Update commited ops if possible and return whether applied
func (doc *Doc) apply(op Op, temp bool) bool {
	if op.Seq == doc.UserSeqs[op.Client]+1 || (op.Type == Init && op.Session != doc.UserSession[op.Client]) {
		switch op.Type {
		case Insert:
			editorInsertRune(doc, op.Client, op.Data, temp)
			break
		case Init:
			if doc.UserSeqs[op.Client] == 0 {
				doc.Colors[op.Client] = len(doc.Colors) + 1
			}
			doc.UserPos[op.Client] = Pos{}
			break
		case Move:
			editorMoveCursor(doc, op.Client, op.Move)
			break
		case Delete:
			editorDelRune(doc, op.Client)
			break
		case Newline:
			editorInsertNewLine(doc, op.Client)
			break
		default:
			return false
		}

		doc.View++
		doc.UserSeqs[op.Client]++
		return true
	}
	return false
}

/*** input ***/

func editorMoveCursor(doc *Doc, id int, key termbox.Key) {
	pos := doc.UserPos[id]
	var row *erow
	if pos.Y < len(doc.Rows) {
		row = &doc.Rows[pos.Y]
	} else {
		row = nil
	}

	oldRx := editorRowCxToRx(&doc.Rows[pos.Y], pos.X)

	switch key {
	case termbox.KeyArrowRight:
		if row != nil && pos.X < len(row.Chars) {
			pos.X++
		} else if row != nil && pos.X >= len(row.Chars) && pos.Y < len(doc.Rows)-1 {
			pos.Y++
			pos.X = 0
		}
		doc.UserPos[id] = pos
		return
	case termbox.KeyArrowLeft:
		if pos.X != 0 {
			pos.X--
		} else if pos.Y > 0 {
			pos.Y--
			pos.X = len(doc.Rows[pos.Y].Chars)
		}
		doc.UserPos[id] = pos
		return
	case termbox.KeyArrowDown:
		if pos.Y < len(doc.Rows)-1 {
			pos.Y++
		}
		doc.UserPos[id] = pos
	case termbox.KeyArrowUp:
		if pos.Y != 0 {
			pos.Y--
		}
		doc.UserPos[id] = pos
	case termbox.KeyHome:
		pos.X = 0
		doc.UserPos[id] = pos
		return
	case termbox.KeyEnd:
		if pos.Y < len(doc.Rows) {
			pos.X = len(doc.Rows[pos.Y].Chars)
		}
		doc.UserPos[id] = pos
		return
	}

	rowlen := 0
	if pos.Y < len(doc.Rows) {
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
	doc.UserPos[id] = pos
}

/*** editor operations ***/

func editorInsertRune(doc *Doc, id int, key rune, temp bool) {
	pos := doc.UserPos[id]

	if pos.Y == len(doc.Rows) {
		doc.insertRow(pos.Y, "", []bool{}, []int{})
	}

	doc.rowInsertRune(pos.X, pos.Y, key, id, temp)

	for k, npos := range doc.UserPos {
		// update other positions
		if k != id {
			if pos.Y == npos.Y && pos.X < npos.X {
				npos.X++
			}
			doc.UserPos[k] = npos
		}
	}

	pos.X++
	doc.UserPos[id] = pos
}

func editorInsertNewLine(doc *Doc, id int) {
	pos := doc.UserPos[id]

	if pos.X == 0 {
		doc.insertRow(pos.Y, "", []bool{}, []int{})
	} else {
		row := &doc.Rows[pos.Y]
		t := make([]bool, len(row.Temp)-pos.X)
		a := make([]int, len(row.Temp)-pos.X)
		copy(t, row.Temp[pos.X:])
		copy(a, row.Author[pos.X:])
		doc.insertRow(pos.Y+1, row.Chars[pos.X:], t, a)
		doc.Rows[pos.Y].Chars = row.Chars[:pos.X]
		doc.Rows[pos.Y].Temp = row.Temp[:pos.X]
		doc.Rows[pos.Y].Author = row.Author[:pos.X]
	}

	for k, npos := range doc.UserPos {
		// update other positions
		if k != id {
			if pos.Y == npos.Y && pos.X <= npos.X {
				npos.Y++
				npos.X -= pos.X
			} else if pos.Y < npos.Y {
				npos.Y++
			}
			doc.UserPos[k] = npos
		}
	}

	pos.Y++
	pos.X = 0
	doc.UserPos[id] = pos
}

func editorDelRune(doc *Doc, id int) {
	pos := doc.UserPos[id]
	if pos.Y == len(doc.Rows) {
		return
	}
	if pos.X == 0 && pos.Y == 0 {
		return
	}

	if pos.X > 0 {
		doc.rowDelRune(pos.X, pos.Y)

		for k, npos := range doc.UserPos {
			// update other positions
			if k != id {
				if pos.Y == npos.Y && pos.X <= npos.X {
					npos.X--
				}
			}
			doc.UserPos[k] = npos
		}
		pos.X--
	} else {
		oldOffset := len(doc.Rows[pos.Y-1].Chars)
		doc.editorDelRow(pos.Y)

		for k, npos := range doc.UserPos {
			// update other positions
			if k != id {
				if pos.Y == npos.Y {
					npos.X += oldOffset
					npos.Y--

				} else if pos.Y < npos.Y {
					npos.Y--
				}
				doc.UserPos[k] = npos
			}
		}

		pos.X = oldOffset
		pos.Y--
	}

	doc.UserPos[id] = pos
}

/*** row operations ***/

func (doc *Doc) insertRow(at int, ch string, temp []bool, auth []int) {
	// doc.Rows = append(doc.Rows, erow{Chars: s})
	doc.Rows = append(doc.Rows, erow{})
	copy(doc.Rows[at+1:], doc.Rows[at:])
	doc.Rows[at] = erow{Chars: ch, Temp: temp, Author: auth}
}

// insert rune into row[aty] at position atx
func (doc *Doc) rowInsertRune(atx, aty int, key rune, id int, temp bool) {
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
	row.Author[atx] = doc.Colors[id]

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
	if at < 0 || at >= len(doc.Rows) {
		return
	}

	doc.Rows[at-1].Chars += doc.Rows[at].Chars
	doc.Rows[at-1].Temp = append(doc.Rows[at-1].Temp, doc.Rows[at].Temp...)
	doc.Rows[at-1].Author = append(doc.Rows[at-1].Author, doc.Rows[at].Author...)
	copy(doc.Rows[at:], doc.Rows[at+1:])
	doc.Rows = doc.Rows[:len(doc.Rows)-1]
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
	curRx := 0
	cx := 0
	for ; cx < len(row.Chars); cx++ {
		if row.Chars[cx] == '\t' {
			curRx += (TABSTOP - 1) - (curRx % TABSTOP)
		}
		curRx++

		if curRx > rx {
			return cx
		}
	}
	return cx
}
