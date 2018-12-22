package main

// import (
// 	"bytes"
// "bufio"
// "net"
// "sync"
// "log"
// )

const (
	Port = ":6060"
)

const (
	Insert = iota
	Delete
	Newline
)

type Err string

type erow struct {
	Chars string
	Temp  []bool
	// render string
}

type Pos struct {
	X, Y int
}

type Doc struct {
	Rows []erow
	View uint32

	// optional really
	Numrows int
}

type Op struct {
	Op     int
	Data   rune
	X, Y   int
	View   uint32
	ID     int
	Client int
}

type Arg struct {
	Op   string
	Data []byte
}

type Reply struct {
	Data []byte
	Err  Err
}

/*** copy functions ***/

func (row *erow) copy() *erow {
	t := make([]bool, len(row.Temp))
	copy(t, row.Temp)
	return &erow{Chars: row.Chars, Temp: t}
}

func (doc *Doc) copy() *Doc {
	d := Doc{View: doc.View, Numrows: doc.Numrows}
	d.Rows = make([]erow, len(doc.Rows))
	for i := 0; i < len(d.Rows); i++ {
		d.Rows[i] = *doc.Rows[i].copy()
	}
	return &d
}

/*** row operations ***/

func (doc *Doc) insertRow(at int, s string, t []bool) {
	// doc.Rows = append(doc.Rows, erow{Chars: s})
	doc.Rows = append(doc.Rows, erow{})
	copy(doc.Rows[at+1:], doc.Rows[at:])
	doc.Rows[at] = erow{Chars: s, Temp: t}
	// doc.Rows[doc.Numrows].render = renderRow(s)
	doc.Numrows++
}

// insert rune into row[aty] at position atx
func (doc *Doc) rowInsertRune(atx, aty int, key rune, temp bool) {
	row := &doc.Rows[aty]

	if atx < 0 || atx > len(row.Chars) {
		atx = len(row.Chars)
	}

	s := row.Chars[0:atx]
	s += string(key)
	s += row.Chars[atx:]
	row.Chars = s

	row.Temp = append(row.Temp, false)
	copy(row.Temp[atx:], row.Temp[atx+1:])
	row.Temp[atx] = temp
}

// delete a rune
func (doc *Doc) rowDelRune(atx, aty int) {
	row := &doc.Rows[aty]

	if atx < 0 || atx > len(row.Chars) {
		atx = len(row.Chars)
	}

	row.Chars = row.Chars[0:atx-1] + row.Chars[atx:]
	row.Temp = append(row.Temp[0:atx-1], row.Temp[atx:]...)
}

func (doc *Doc) editorDelRow(at int) {
	if at < 0 || at >= doc.Numrows {
		return
	}

	doc.Rows[at-1].Chars += doc.Rows[at].Chars
	copy(doc.Rows[at:], doc.Rows[at+1:])
	doc.Rows = doc.Rows[:len(doc.Rows)-1]
	doc.Numrows--
}
