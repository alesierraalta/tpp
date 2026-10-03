package main

import (
	"os"
	"testing"
)

// TestMain points the installation state at a throwaway directory, so no test in this package
// can read or rewrite the state of the tpp installed on the machine running the suite.
func TestMain(m *testing.M) {
	root, err := os.MkdirTemp("", "tpp-state-")
	if err != nil {
		panic(err)
	}
	os.Setenv("TSP_HOME", "")
	os.Setenv("TPP_HOME", root)
	code := m.Run()
	os.RemoveAll(root)
	os.Exit(code)
}
