package server

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

// Adding a peer always reported success while the peer never appeared in the
// list (issue #38). LND's ConnectToPeer has two branches and only one of them
// answers the question the UI is asking:
//
//	if perm {
//	    ...
//	    go s.connMgr.Connect(connReq)
//	    return nil            // returns before anything is dialled
//	}
//	s.connectToPeer(addr, errChan, timeout)
//	return <-errChan          // the real dial result
//
// So a permanent request succeeds for any syntactically valid pubkey@host, even
// an unreachable one. The outcome has to be confirmed separately.
func TestPermanentConnectIsConfirmedNotAssumed(t *testing.T) {
	raw, err := os.ReadFile("handlers.go")
	if err != nil {
		t.Fatalf("read handlers.go: %v", err)
	}
	source := string(raw)

	start := strings.Index(source, "func (s *Server) handleLNConnectPeer(")
	if start < 0 {
		t.Fatal("handleLNConnectPeer not found")
	}
	end := strings.Index(source[start:], "\nfunc ")
	if end < 0 {
		t.Fatal("could not bound handleLNConnectPeer")
	}
	body := source[start : start+end]

	if !strings.Contains(body, "waitForPeerConnection") {
		t.Error("the handler must confirm the connection landed; a permanent " +
			"ConnectPeer returns nil before LND dials anything")
	}
	if !strings.Contains(body, `"connected"`) {
		t.Error("the response must report whether the peer actually connected, " +
			"not only that the request was accepted")
	}
}

// Every other caller dials for real and gets the true error. Only the manual
// Add peer button asks for a permanent connection, which is why it was the only
// one that could lie.
func TestOtherCallersDialForReal(t *testing.T) {
	call := regexp.MustCompile(`ConnectPeerWithTimeout\(\s*[^,]+,\s*[^,]+,\s*[^,]+,\s*(true|false)`)
	for _, name := range []string{"handlers.go", "balanced_open_service.go", "chan_status_healer.go", "magma_execution.go"} {
		raw, err := os.ReadFile(name)
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		for _, match := range call.FindAllStringSubmatch(string(raw), -1) {
			if match[1] == "true" {
				t.Errorf("%s calls ConnectPeerWithTimeout with perm=true; that returns "+
					"before dialling, so the result must be confirmed against ListPeers", name)
			}
		}
	}
}
