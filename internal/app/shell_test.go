package app

import (
	"errors"
	"testing"
)

// TestResolveShell 覆盖解释器回退的关键路径：
// Linux/fnOS 有 bash 时用 bash；Android 等只有 POSIX sh 时回退 sh；
// 两者都没有时保底返回 bash（保持上游报错语义）。
func TestResolveShell(t *testing.T) {
	notFound := errors.New("executable file not found in $PATH")

	cases := []struct {
		name string
		look func(string) (string, error)
		want string
	}{
		{
			name: "存在 bash 时优先 bash",
			look: func(f string) (string, error) {
				if f == "bash" {
					return "/usr/bin/bash", nil
				}
				return "", notFound
			},
			want: "/usr/bin/bash",
		},
		{
			name: "无 bash（Android）时回退 sh",
			look: func(f string) (string, error) {
				if f == "sh" {
					return "/system/bin/sh", nil
				}
				return "", notFound
			},
			want: "/system/bin/sh",
		},
		{
			name: "两者都没有时保底 bash",
			look: func(string) (string, error) { return "", notFound },
			want: "bash",
		},
		{
			name: "查找结果为空串时视为不可用",
			look: func(string) (string, error) { return "", nil },
			want: "bash",
		},
	}

	for _, c := range cases {
		if got := resolveShell(c.look); got != c.want {
			t.Errorf("%s: resolveShell() = %q, want %q", c.name, got, c.want)
		}
	}
}
