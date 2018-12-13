package main

import (
	"bufio"
	"net"
	"sync"
)

type erow struct {
	Chars string
	// render string
}

type gopad struct {
	screenrows int
	screencols int
	numrows    int
	rowoff     int
	coloff     int
	// rx         int
	cx   int
	cy   int
	rows []erow
	buf  *bufio.ReadWriter
	mu   sync.Mutex
}

const (
	Port = ":6060"
)

type server struct {
	listener net.Listener
	doc      Doc

	// handler  map[string]HandleFunc
	// m sync.RWMutex
}

type Doc struct {
	Rows []erow
}
