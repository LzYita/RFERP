//go:build windows

package singleinstance

import "testing"

func TestAcquireSecondFails(t *testing.T) {
	name := "RFERP.Test.SingleInstance"
	ok, err := Acquire(name)
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatal("first acquire should succeed")
	}
	ok2, err := Acquire(name)
	if err != nil {
		t.Fatal(err)
	}
	if ok2 {
		t.Fatal("second acquire should fail")
	}
}
