package logx

import (
	"strings"
	"testing"
)

func TestRedact(t *testing.T) {
	cases := []struct {
		input    string
		contains string
		excludes string
	}{
		{
			input:    "oracle://admin:secret123@adb.ap-tokyo-1.oraclecloud.com:1522/svc",
			contains: "oracle://admin:***@adb.ap-tokyo-1.oraclecloud.com:1522/svc",
			excludes: "secret123",
		},
		{
			input:    "root:my_password@tcp(127.0.0.1:3306)/dash_test",
			contains: "***",
			excludes: "my_password",
		},
		{
			input:    "connection failed: password=super_secret host=localhost",
			contains: "password=***",
			excludes: "super_secret",
		},
	}

	for _, c := range cases {
		got := Redact(c.input)
		if !strings.Contains(got, c.contains) {
			t.Errorf("Redact(%q) = %q, expected to contain %q", c.input, got, c.contains)
		}
		if strings.Contains(got, c.excludes) {
			t.Errorf("Redact(%q) = %q, must NOT contain secret %q", c.input, got, c.excludes)
		}
	}
}
