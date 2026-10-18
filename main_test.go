package main

import "testing"

func TestServerPort(t *testing.T) {
	if got := serverPort(0, 9000); got != 9000 {
		t.Fatalf("expected configured port 9000, got %d", got)
	}
	if got := serverPort(8080, 9000); got != 8080 {
		t.Fatalf("expected flag port 8080, got %d", got)
	}
}
