package privileged

import "testing"

func TestBoundedOutput(t *testing.T) {
	output := newBoundedOutput(4)
	if written, err := output.Write([]byte("abcdef")); err != nil || written != 6 {
		t.Fatalf("Write() = %d, %v", written, err)
	}
	if got := output.String(); got != "abcd" {
		t.Fatalf("output = %q, want abcd", got)
	}
	if !output.Overflowed() {
		t.Fatal("expected overflow to be recorded")
	}
}
