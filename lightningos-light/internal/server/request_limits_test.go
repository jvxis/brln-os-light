package server

import (
	"bufio"
	"bytes"
	"crypto/tls"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestRequestBodyTransportLimits(t *testing.T) {
	for _, h2 := range []bool{false, true} {
		name := "http1"
		if h2 {
			name = "http2"
		}
		t.Run(name, func(t *testing.T) {
			var calls atomic.Int32
			h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				_, _ = io.Copy(io.Discard, r.Body)
				w.WriteHeader(http.StatusNoContent)
			})
			logger := (&Server{logger: log.New(io.Discard, "", 0)}).requestLogger()
			s := httptest.NewUnstartedServer(logger(requestBodyMiddleware(100 * time.Millisecond)(h)))
			s.EnableHTTP2 = h2
			s.StartTLS()
			defer s.Close()
			for _, chunked := range []bool{false, true} {
				before := calls.Load()
				payload := strings.NewReader(`{"valid":"prefix"}` + strings.Repeat(" ", 1<<20))
				req, _ := http.NewRequest("POST", s.URL+"/api/wallet/send", payload)
				if chunked {
					req.ContentLength = -1
				}
				res, err := s.Client().Do(req)
				if err != nil {
					t.Fatal(err)
				}
				res.Body.Close()
				if h2 && res.ProtoMajor != 2 {
					t.Fatal("HTTP/2 was not negotiated")
				}
				if res.StatusCode != 413 || calls.Load() != before {
					t.Fatalf("oversized (chunked=%v): status=%d handler calls=%d", chunked, res.StatusCode, calls.Load()-before)
				}
			}
			for _, tc := range []struct {
				path         string
				size, status int
			}{
				{"/api/auth/login", 17000, 413},
				{"/api/auth/login", 16000, 204},
				{"/api/lnops/channel/scb/restore", 2 << 20, 204},
				{"/api/lnops/channel/scb/restore", (16 << 20) + 1, 413},
				{"/api/financial/intents", 2 << 20, 204},
				{"/api/financial/intents", (16 << 20) + (64 << 10), 204},
				{"/api/financial/intents", (16 << 20) + (64 << 10) + 1, 413},
				{"/api/reports/config", 3000, 204},
			} {
				res, err := s.Client().Post(s.URL+tc.path, "application/json", bytes.NewReader(bytes.Repeat([]byte(" "), tc.size)))
				if err != nil {
					t.Fatal(err)
				}
				res.Body.Close()
				if res.StatusCode != tc.status {
					t.Fatalf("%s size=%d: got %d want %d", tc.path, tc.size, res.StatusCode, tc.status)
				}
			}
			before := calls.Load()
			reader, writer := io.Pipe()
			defer reader.Close()
			done := make(chan struct{})
			go func() {
				defer close(done)
				_, _ = writer.Write([]byte("{"))
				time.Sleep(350 * time.Millisecond)
				writer.Close()
			}()
			req, _ := http.NewRequest("POST", s.URL+"/api/wallet/send", reader)
			start := time.Now()
			res, err := s.Client().Do(req)
			if err != nil {
				t.Fatal(err)
			}
			res.Body.Close()
			if res.StatusCode != 408 || calls.Load() != before || time.Since(start) > time.Second {
				t.Fatalf("slow body: status=%d calls=%d duration=%s", res.StatusCode, calls.Load()-before, time.Since(start))
			}
			<-done
		})
	}
}

func TestBodyBudgetConcurrentUploads(t *testing.T) {
	entered := make(chan struct{})
	release := make(chan struct{})
	var calls atomic.Int32
	h := requestBodyMiddleware(time.Second)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) == 1 {
			close(entered)
			<-release
		}
		w.WriteHeader(204)
	}))
	s := httptest.NewTLSServer(h)
	defer s.Close()
	done := make(chan error, 1)
	go func() {
		res, err := s.Client().Post(s.URL+"/api/lnops/channel/scb/restore", "application/json", strings.NewReader("{}"))
		if err == nil {
			res.Body.Close()
		}
		done <- err
	}()
	<-entered
	res, err := s.Client().Post(s.URL+"/api/lnops/channel/scb/restore", "application/json", strings.NewReader("{}"))
	if err != nil {
		close(release)
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != 503 || res.Header.Get("Retry-After") == "" {
		close(release)
		t.Fatalf("unbounded concurrent uploads: %d", res.StatusCode)
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	res, err = s.Client().Post(s.URL+"/api/lnops/channel/scb/restore", "application/json", strings.NewReader("{}"))
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != 204 {
		t.Fatalf("reservation was not released: %d", res.StatusCode)
	}
}

func TestBodyTimeoutPreservesHijackedTerminal(t *testing.T) {
	h := requestBodyMiddleware(20 * time.Millisecond)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, rw, err := w.(http.Hijacker).Hijack()
		if err != nil {
			return
		}
		defer conn.Close()
		fmt.Fprint(rw, "HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nConnection: Upgrade\r\n\r\n")
		rw.Flush()
		line, err := rw.ReadString('\n')
		if err == nil {
			fmt.Fprint(rw, line)
			rw.Flush()
		}
	}))
	s := httptest.NewUnstartedServer(h)
	s.Config.ReadTimeout = 20 * time.Millisecond
	s.StartTLS()
	defer s.Close()
	conn, err := tls.Dial("tcp", s.Listener.Addr().String(), s.Client().Transport.(*http.Transport).TLSClientConfig)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(2 * time.Second))
	fmt.Fprint(conn, "GET /terminal/ws HTTP/1.1\r\nHost: localhost\r\nUpgrade: websocket\r\nConnection: Upgrade\r\n\r\n")
	rw := bufio.NewReader(conn)
	res, err := http.ReadResponse(rw, nil)
	if err != nil {
		t.Fatal(err)
	}
	if res.StatusCode != 101 {
		t.Fatal(res.StatusCode)
	}
	time.Sleep(80 * time.Millisecond)
	fmt.Fprint(conn, "still alive\n")
	line, err := rw.ReadString('\n')
	if err != nil || line != "still alive\n" {
		t.Fatalf("terminal stream interrupted: %q %v", line, err)
	}
}

func TestBodyBudgetDoesNotLimitResponseLifetime(t *testing.T) {
	for _, h2 := range []bool{false, true} {
		s := httptest.NewUnstartedServer(requestBodyMiddleware(20 * time.Millisecond)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/event-stream")
			w.(http.Flusher).Flush()
			time.Sleep(80 * time.Millisecond)
			_, _ = io.WriteString(w, "data: complete\n\n")
		})))
		s.EnableHTTP2 = h2
		s.StartTLS()
		res, err := s.Client().Get(s.URL + "/api/notifications/stream")
		if err != nil {
			t.Fatal(err)
		}
		body, err := io.ReadAll(res.Body)
		res.Body.Close()
		s.Close()
		if err != nil || string(body) != "data: complete\n\n" {
			t.Fatalf("long response failed: %q %v", body, err)
		}
	}
}
