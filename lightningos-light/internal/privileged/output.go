package privileged

import "bytes"

type boundedOutput struct {
	buffer   bytes.Buffer
	limit    int
	overflow bool
}

func newBoundedOutput(limit int) *boundedOutput {
	return &boundedOutput{limit: limit}
}

func (output *boundedOutput) Write(data []byte) (int, error) {
	written := len(data)
	remaining := output.limit - output.buffer.Len()
	if remaining <= 0 {
		output.overflow = output.overflow || written > 0
		return written, nil
	}
	if len(data) > remaining {
		_, _ = output.buffer.Write(data[:remaining])
		output.overflow = true
		return written, nil
	}
	_, _ = output.buffer.Write(data)
	return written, nil
}

func (output *boundedOutput) Bytes() []byte    { return output.buffer.Bytes() }
func (output *boundedOutput) String() string   { return output.buffer.String() }
func (output *boundedOutput) Overflowed() bool { return output.overflow }
