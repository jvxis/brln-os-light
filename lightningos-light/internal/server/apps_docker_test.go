package server

import (
	"errors"
	"strings"
	"testing"
)

func TestComposeCommandErrorDetailPreservesCauseAndRedactsSecrets(t *testing.T) {
	out := strings.Join([]string{
		"building app",
		"POSTGRES_PASSWORD=do-not-leak",
		"ERROR: No matching distribution found for django==6.0.6",
	}, "\n")

	detail := composeCommandErrorDetail(out, errors.New("exit status 1"))
	if !strings.Contains(detail, "No matching distribution found") {
		t.Fatalf("actionable Compose failure missing from %q", detail)
	}
	if strings.Contains(detail, "do-not-leak") {
		t.Fatalf("Compose failure leaked a secret: %q", detail)
	}
	if !strings.Contains(detail, "[redacted]") {
		t.Fatalf("Compose failure did not mark the redaction: %q", detail)
	}
}

func TestComposeCommandErrorDetailUsesBoundedTail(t *testing.T) {
	lines := make([]string, 0, composeErrorMaxLines+10)
	for i := 0; i < composeErrorMaxLines+9; i++ {
		lines = append(lines, "old build output")
	}
	lines = append(lines, "final actionable failure")

	detail := composeCommandErrorDetail(strings.Join(lines, "\n"), errors.New("exit status 1"))
	if !strings.Contains(detail, "final actionable failure") {
		t.Fatalf("Compose failure tail missing from %q", detail)
	}
	if got := len(strings.Split(detail, "\n")); got > composeErrorMaxLines {
		t.Fatalf("Compose failure returned %d lines, max %d", got, composeErrorMaxLines)
	}
}

func TestComposeStartsDetached(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want bool
	}{
		{name: "short detach", args: []string{"up", "-d"}, want: true},
		{name: "long detach", args: []string{"up", "--detach", "service"}, want: true},
		{name: "no start", args: []string{"up", "--no-start"}, want: false},
		{name: "stop", args: []string{"stop"}, want: false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := composeStartsDetached(test.args); got != test.want {
				t.Fatalf("composeStartsDetached(%#v) = %v, want %v", test.args, got, test.want)
			}
		})
	}
}

func TestDockerContainerIDValidation(t *testing.T) {
	valid := []string{
		"0123456789ab",
		"abcdef0123456789abcdef0123456789abcdef0123456789abcdef0123456789",
	}
	for _, value := range valid {
		if !isDockerContainerID(value) {
			t.Fatalf("expected valid container id: %q", value)
		}
	}
	invalid := []string{"short", "container-name", "0123456789ag", "0123456789ab;reboot"}
	for _, value := range invalid {
		if isDockerContainerID(value) {
			t.Fatalf("expected invalid container id: %q", value)
		}
	}
}
