package testing

import "github.com/ilnaes/gopad/src"

import "testing"

func TestInput(t *testing.T) {
	s := gopad.NewServer("")
	go s.Start()

	gopad.StartClient(1, "localhost", 6060, true)

}
