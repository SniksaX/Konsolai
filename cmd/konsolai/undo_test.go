package main

import "testing"

func TestShouldForceRestore(t *testing.T) {
	askRestore = func() (string, error) { return "r", nil }
	t.Cleanup(func() { askRestore = confirmRestoreAnyway })
	ok, err := shouldForceRestore(false)
	if err != nil || !ok {
		t.Fatalf("restore %v %v", ok, err)
	}
	ok, err = shouldForceRestore(true)
	if err != nil || ok {
		t.Fatalf("--yes skips %v %v", ok, err)
	}
	askRestore = func() (string, error) { return "s", nil }
	ok, err = shouldForceRestore(false)
	if err != nil || ok {
		t.Fatalf("skip %v %v", ok, err)
	}
}
