package privileged

import (
	"context"
	"fmt"
	"os/exec"
)

// Responses must remain below the protocol's 64 KiB ceiling after command
// output is JSON-escaped into the broker envelope. Tapd's typed balance and
// universe reads can legitimately exceed the old 16 KiB cap; 28 KiB leaves
// room for worst-case quote/backslash doubling plus the response metadata.
const maxCommandOutputBytes = 28 * 1024

type ExecCommandRunner struct{}

func (runner *ExecCommandRunner) Run(ctx context.Context, path string, args ...string) (string, error) {
	command := exec.CommandContext(ctx, path, args...)
	output := newBoundedOutput(maxCommandOutputBytes)
	command.Stdout = output
	command.Stderr = output
	err := command.Run()
	if err != nil {
		return output.String(), fmt.Errorf("fixed privileged command failed: %w", err)
	}
	return output.String(), nil
}
