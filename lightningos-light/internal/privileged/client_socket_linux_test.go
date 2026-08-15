//go:build linux

package privileged

import (
	"context"
	"encoding/json"
	"net"
	"path/filepath"
	"testing"
	"time"
)

func TestSocketTransportFramesOneBrokerRequest(t *testing.T) {
	path := filepath.Join(t.TempDir(), "broker.sock")
	listener, err := net.Listen("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()

	received := make(chan Request, 1)
	serverErr := make(chan error, 1)
	go func() {
		connection, acceptErr := listener.Accept()
		if acceptErr != nil {
			serverErr <- acceptErr
			return
		}
		defer connection.Close()
		request, decodeErr := DecodeRequest(connection)
		if decodeErr != nil {
			serverErr <- decodeErr
			return
		}
		received <- request
		result, marshalErr := json.Marshal(map[string]bool{"ready": true})
		if marshalErr != nil {
			serverErr <- marshalErr
			return
		}
		serverErr <- json.NewEncoder(connection).Encode(Response{
			Version: ProtocolVersion, RequestID: request.RequestID, OK: true, Result: result,
		})
	}()

	params, err := MarshalParams(struct{}{})
	if err != nil {
		t.Fatal(err)
	}
	request := Request{Version: ProtocolVersion, RequestID: "socket_test_1", Operation: OperationSelfTest, Params: params}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	response, err := (&SocketTransport{Path: path}).Do(ctx, request)
	if err != nil {
		t.Fatal(err)
	}
	if !response.OK || response.RequestID != request.RequestID {
		t.Fatalf("unexpected broker response: %+v", response)
	}
	if got := <-received; got.RequestID != request.RequestID || got.Operation != OperationSelfTest {
		t.Fatalf("unexpected broker request: %+v", got)
	}
	if err := <-serverErr; err != nil {
		t.Fatal(err)
	}
}
