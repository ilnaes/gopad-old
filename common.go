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

type Err string

type erow struct {
	Chars string
	Temp  []bool
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

type Op struct {
	Op     string
	Data   rune
	X, Y   int
	View   int
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
