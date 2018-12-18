package main

// import (
// 	"bytes"
// "bufio"
// "net"
// "sync"
// )

const (
	Port = ":6060"
)

type Err string

type erow struct {
	Chars string
	// render string
}

type pos struct {
	X, Y int
}

type Doc struct {
	Rows    []erow
	View    int
	Cursors []pos

	// optional really
	Numrows int
}

type Commit struct {
	Op     string
	Data   rune
	X, Y   int
	View   int
	ID     int64
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

/*** row operations ***/

func (doc *Doc) insertRow(at int, s string) {
	// doc.Rows = append(doc.Rows, erow{Chars: s})
	doc.Rows = append(doc.Rows, erow{})
	copy(doc.Rows[at+1:], doc.Rows[at:])
	doc.Rows[at] = erow{Chars: s}
	// doc.Rows[doc.Numrows].render = renderRow(s)
	doc.Numrows++
}

func (doc *Doc) rowInsertRune(atx, aty int, key rune) {
	row := &doc.Rows[aty]

	if atx < 0 || atx > len(row.Chars) {
		atx = len(row.Chars)
	}

	s := row.Chars[0:atx]
	s += string(key)
	s += row.Chars[atx:]
	row.Chars = s
}

func (doc *Doc) rowDelRune(atx, aty int) {
	row := &doc.Rows[aty]

	if atx < 0 || atx > len(row.Chars) {
		atx = len(row.Chars)
	}

	s := row.Chars[0:atx-1] + row.Chars[atx:]
	row.Chars = s
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
