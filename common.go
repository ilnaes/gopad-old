package main

import (
	"encoding/binary"
	// 	"bytes"
	// "bufio"
	// "net"
	// "sync"
	// "log"
)

const (
	Port = ":6060"
)

const (
	Insert = iota
	Delete
	Newline
	Init
	Left
	Right
	Up
	Down
	Home
	End
)

type Err string

type erow struct {
	Chars  string
	Temp   []bool
	Author []uint32
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
	Type int
	Data rune
	// X, Y   int
	View   uint32
	ID     uint32
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

func intToByte(x uint32) []byte {
	buf := make([]byte, binary.MaxVarintLen32)
	binary.LittleEndian.PutUint32(buf, x)
	return buf
}

func byteToInt(buf []byte) uint32 {
	return binary.LittleEndian.Uint32(buf)
}

/*** copy functions ***/

func (row *erow) copy() *erow {
	t := make([]bool, len(row.Temp))
	copy(t, row.Temp)
	a := make([]uint32, len(row.Author))
	copy(a, row.Author)
	return &erow{Chars: row.Chars, Temp: t, Author: a}
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

func (doc *Doc) insertRow(at int, s string, t []bool, a []uint32) {
	// doc.Rows = append(doc.Rows, erow{Chars: s})
	doc.Rows = append(doc.Rows, erow{})
	copy(doc.Rows[at+1:], doc.Rows[at:])
	doc.Rows[at] = erow{Chars: s, Temp: t, Author: a}
	// doc.Rows[doc.Numrows].render = renderRow(s)
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
