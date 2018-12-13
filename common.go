package main

// import (
// "bufio"
// "net"
// "sync"
// )

const (
	Port = ":6060"
)

type erow struct {
	Chars string
	// render string
}

type Doc struct {
	Rows []erow
	// optional really
	numrows int
}

/*** row operations ***/

func (doc *Doc) insertRow(at int, s string) {
	// doc.Rows = append(doc.Rows, erow{Chars: s})
	doc.Rows = append(doc.Rows, erow{})
	copy(doc.Rows[at+1:], doc.Rows[at:])
	doc.Rows[at] = erow{Chars: s}
	// doc.Rows[doc.numrows].render = renderRow(s)
	doc.numrows++
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
	if at < 0 || at >= doc.numrows {
		return
	}

	doc.Rows[at-1].Chars += doc.Rows[at].Chars
	copy(doc.Rows[at:], doc.Rows[at+1:])
	doc.Rows = doc.Rows[:len(doc.Rows)-1]
	doc.numrows--
}
