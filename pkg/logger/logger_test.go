package logger

import (
	"bytes"
	"strings"
	"testing"
)

func TestLogger(t *testing.T) {
	buf := new(bytes.Buffer)
	// InitLogger(buf)

	tests := []struct {
		logFunc func(string, ...interface{})
		level   string
		message string
	}{
		{Info, "INFO", "Test info message"},
		{Error, "ERROR", "Test error message"},
		{Debug, "DEBUG", "Test debug message"},
	}

	for _, test := range tests {
		buf.Reset()
		test.logFunc(test.message)
		output := buf.String()

		if !strings.Contains(output, test.level) {
			t.Errorf("Expected log level %s, but got: %s", test.level, output)
		}
		if !strings.Contains(output, test.message) {
			t.Errorf("Expected message '%s', but got: %s", test.message, output)
		}
	}
}