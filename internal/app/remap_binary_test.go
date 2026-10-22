package app

import (
	"bytes"
	"compress/gzip"
	"os"
	"path/filepath"
	"testing"
)

// 归档/压缩文件不得被当作脚本处理。
//
// 真实故障：Agent2API 3.1.0 的 target/server/agent2api.tar.gz 被
// isScriptFile 判为脚本（未知扩展名兜底为 true），又因
// isBinaryContent 只看控制字符比例（gzip 约 28%，低于 30% 阈值）
// 而未被拦住，最终 normalizeLineEndings 把二进制中的 CRLF 折叠成 LF，
// 内层包少了 119 字节，install_callback 的 tar -xzf 直接失败，
// 解出的服务端二进制只剩 224KB，应用装得上但起不来。
func TestIsScriptFileSkipsArchives(t *testing.T) {
	skip := []string{
		"agent2api.tar.gz", "app.tgz", "server.tar", "x.zip", "y.7z",
		"z.xz", "a.bz2", "b.zst", "c.whl", "d.jar", "e.so", "f.bin",
		"g.cgi", "h.json", "i.png", "j.db", "k.js",
	}
	for _, n := range skip {
		if isScriptFile(n) {
			t.Errorf("%s 不应被当作脚本", n)
		}
	}

	keep := []string{"main", "install_callback", "uninstall_callback", "start.sh", "restart.bash", "hook.py", "run.pl"}
	for _, n := range keep {
		if !isScriptFile(n) {
			t.Errorf("%s 应被当作脚本", n)
		}
	}
}

// gzip 压缩数据必须被识别为二进制（不能只看控制字符比例）
func TestIsBinaryContentDetectsGzip(t *testing.T) {
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	// 用高度可压缩的文本填充，让压缩流的控制字符占比尽量低 —— 这正是
	// 旧实现漏判的情形。
	gz.Write(bytes.Repeat([]byte("hello world, this is text.\n"), 4000))
	gz.Close()

	if !isBinaryContent(buf.Bytes()) {
		t.Errorf("gzip 数据应判定为二进制（头部含 NUL），前 8 字节: % x", buf.Bytes()[:8])
	}

	// 纯文本不能被误判
	if isBinaryContent([]byte("#!/bin/bash\necho hi\n")) {
		t.Error("纯文本不应判定为二进制")
	}
}

// 端到端：remapScriptPaths 不能改动 target 下的压缩包
func TestRemapScriptPathsKeepsArchivesIntact(t *testing.T) {
	appDir := t.TempDir()
	srvDir := filepath.Join(appDir, "target", "server")
	if err := os.MkdirAll(srvDir, 0o755); err != nil {
		t.Fatal(err)
	}

	// 构造一段带 CRLF 的压缩数据，模拟真实内层包
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	gz.Write(bytes.Repeat([]byte("line one\r\nline two\r\n"), 2000))
	gz.Close()
	archive := buf.Bytes()

	pkgPath := filepath.Join(srvDir, "agent2api.tar.gz")
	if err := os.WriteFile(pkgPath, archive, 0o644); err != nil {
		t.Fatal(err)
	}

	// 同时放一个真正需要重映射的脚本
	scriptPath := filepath.Join(appDir, "target", "run.sh")
	script := "#!/bin/bash\r\nexec /var/apps/demo/bin/app\r\n"
	if err := os.WriteFile(scriptPath, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}

	m := &Manager{}
	m.remapScriptPaths(appDir, "demo")

	// 压缩包必须逐字节不变
	got, err := os.ReadFile(pkgPath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, archive) {
		t.Errorf("压缩包被改动：原 %d 字节，现 %d 字节", len(archive), len(got))
	}

	// 脚本应被重映射且 CRLF 归一为 LF
	gs, err := os.ReadFile(scriptPath)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(gs, []byte("\r")) {
		t.Error("脚本的 CRLF 应被归一为 LF")
	}
	if !bytes.Contains(gs, []byte(appDir)) {
		t.Errorf("脚本中的 /var/apps/demo 应被替换为 %s，实际: %s", appDir, gs)
	}
}
