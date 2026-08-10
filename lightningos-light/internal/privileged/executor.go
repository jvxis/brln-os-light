package privileged

import (
	"context"
	"fmt"
	"os/exec"
)

const maxCommandOutputBytes = 16 * 1024

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
