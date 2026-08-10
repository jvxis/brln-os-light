package main

import (
	"context"
	"encoding/json"
	"os"
	"time"

	"lightningos-light/internal/privileged"
)

func main() {
	if len(os.Args) != 1 {
		writeResponse(privileged.ErrorResponse("", "invalid_invocation", "broker accepts no command-line arguments"))
		return
	}
	caller, err := privileged.AuthorizeCaller()
	if err != nil {
		writeResponse(privileged.ErrorResponse("", "unauthorized", "privileged broker caller is not authorized"))
		return
	}
	audit, err := privileged.NewFileAudit(privileged.DefaultAuditPath)
	if err != nil {
		writeResponse(privileged.ErrorResponse("", "audit_unavailable", "privileged audit is unavailable"))
		return
	}
	defer audit.Close()
	locker, err := privileged.NewFileLocker(privileged.DefaultLockPath)
	if err != nil {
		writeResponse(privileged.ErrorResponse("", "lock_unavailable", "privileged operation lock is unavailable"))
		return
	}

	request, err := privileged.DecodeRequest(os.Stdin)
	if err != nil {
		_ = audit.Write(privileged.AuditEvent{
			Timestamp: time.Now().UTC(),
			Phase:     "complete",
			Caller:    caller,
			Operation: "request.invalid",
			Success:   false,
			ErrorCode: "invalid_request",
		})
		writeResponse(privileged.ErrorResponse(request.RequestID, "invalid_request", "invalid privileged broker request"))
		return
	}

	runner := &privileged.ExecCommandRunner{}
	broker := &privileged.Broker{
		Runner:  runner,
		Locker:  locker,
		Audit:   audit,
		Files:   privileged.NewAtomicConfigFiles(privileged.DefaultManagerConfigPath),
		Apps:    privileged.NewComposeAppManager(runner),
		Caller:  caller,
		Timeout: 15 * time.Second,
	}
	writeResponse(broker.Handle(context.Background(), request))
}

func writeResponse(response privileged.Response) {
	encoder := json.NewEncoder(os.Stdout)
	encoder.SetEscapeHTML(true)
	if err := encoder.Encode(response); err != nil {
		os.Exit(1)
	}
}
