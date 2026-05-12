package main

import (
	"testing"

	"github.com/onexstack/cobrax/test"
)

func TestTools(t *testing.T) {
	test.Tools(t, makeCmd(), "make")
}
