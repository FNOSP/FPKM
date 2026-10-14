package app

import (
	"os"
	"path/filepath"
	"testing"
)

// TestUIConfigLaunchPathVsPrefix 覆盖用户报告的要点：
// 统一网关应用的访问地址必须读取 app/ui/config 的 url 字段，
// 而不是拿 gatewayPrefix 直接用。
//
// 真实案例（fnchat）：
//
//	gatewayPrefix = "/app/fnchat"
//	url           = "/app/fnchat/"   ← 打开应用要用这个（带尾斜杠）
func TestUIConfigLaunchPathVsPrefix(t *testing.T) {
	cases := []struct {
		name       string
		uiConfig   string
		wantPrefix string
		wantLaunch string
	}{
		{
			name: "fnchat: url 带尾斜杠",
			uiConfig: `{
			  ".url": {
			    "fnchat.main": {
			      "gatewayPrefix": "/app/fnchat",
			      "gatewaySocket": "app.sock",
			      "url": "/app/fnchat/"
			    }
			  }
			}`,
			wantPrefix: "/app/fnchat",
			wantLaunch: "/app/fnchat/",
		},
		{
			name: "file-collector: url 是子路径",
			uiConfig: `{
			  ".url": {
			    "file-collector.main": {
			      "gatewayPrefix": "/app/file-collector",
			      "gatewaySocket": "app.sock",
			      "url": "/app/file-collector/gateway"
			    }
			  }
			}`,
			wantPrefix: "/app/file-collector",
			wantLaunch: "/app/file-collector/gateway",
		},
		{
			name: "fygo-browser: url 带查询串",
			uiConfig: `{
			  ".url": {
			    "fygo-browser.Application": {
			      "gatewayPrefix": "/app/fygo-browser",
			      "gatewaySocket": "app.sock",
			      "url": "/app/fygo-browser/browser.html?v=1.0.1-fnos"
			    }
			  }
			}`,
			wantPrefix: "/app/fygo-browser",
			wantLaunch: "/app/fygo-browser/browser.html?v=1.0.1-fnos",
		},
		{
			name: "无尾斜杠 url",
			uiConfig: `{
			  ".url": {
			    "fn-appstores-client.Application": {
			      "gatewayPrefix": "/app/fn-appstores-client",
			      "gatewaySocket": "app.sock",
			      "url": "/app/fn-appstores-client"
			    }
			  }
			}`,
			wantPrefix: "/app/fn-appstores-client",
			wantLaunch: "/app/fn-appstores-client",
		},
		{
			name: "只有 url 没有 gatewayPrefix（从前缀推导）",
			uiConfig: `{
			  ".url": {
			    "miyin.main": {
			      "gatewaySocket": "app.sock",
			      "url": "/app/miyin/"
			    }
			  }
			}`,
			wantPrefix: "/app/miyin",
			wantLaunch: "/app/miyin/",
		},
		{
			name: "URL 前缀与 appname 不同（trim.hermes）",
			uiConfig: `{
			  ".url": {
			    "trim.hermes.main": {
			      "gatewayPrefix": "/app/trim-hermes",
			      "gatewaySocket": "app.sock",
			      "url": "/app/trim-hermes/"
			    }
			  }
			}`,
			wantPrefix: "/app/trim-hermes",
			wantLaunch: "/app/trim-hermes/",
		},
		{
			name: "非标准 JSON（尾随逗号）也能解析",
			uiConfig: `{
			  ".url": {
			    "fnchat": {
			      "title": "FnChat",
			      "port": "8033",
			      "gatewayPrefix": "/app/fnchat",
			      "gatewaySocket": "fnchat.sock",
			      "url": "/app/fnchat/",
			    },
			  }
			}`,
			wantPrefix: "/app/fnchat",
			wantLaunch: "/app/fnchat/",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			appDir := filepath.Join(dir, "apps", "testapp")
			targetDir := filepath.Join(appDir, "target")
			if err := os.MkdirAll(filepath.Join(targetDir, "ui"), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(targetDir, "ui", "config"), []byte(tc.uiConfig), 0o644); err != nil {
				t.Fatal(err)
			}

			gotPrefix := ResolveGatewayPrefix(targetDir, appDir, "testapp")
			if gotPrefix != tc.wantPrefix {
				t.Errorf("网关前缀 = %q, 期望 %q", gotPrefix, tc.wantPrefix)
			}

			gotLaunch := ResolveLaunchPath(targetDir, appDir, "testapp")
			if gotLaunch != tc.wantLaunch {
				t.Errorf("打开路径 = %q, 期望 %q", gotLaunch, tc.wantLaunch)
			}
		})
	}
}

// TestResolveLaunchPathFallback 没有 ui/config 时的兜底行为：
// 打开路径应回退为网关前缀并补尾斜杠（保证相对资源解析正确）。
func TestResolveLaunchPathFallback(t *testing.T) {
	dir := t.TempDir()
	appDir := filepath.Join(dir, "apps", "noapp")
	targetDir := filepath.Join(appDir, "target")
	if err := os.MkdirAll(targetDir, 0o755); err != nil {
		t.Fatal(err)
	}

	if got := ResolveGatewayPrefix(targetDir, appDir, "noapp"); got != "/app/noapp" {
		t.Errorf("兜底网关前缀 = %q, 期望 /app/noapp", got)
	}
	if got := ResolveLaunchPath(targetDir, appDir, "noapp"); got != "/app/noapp/" {
		t.Errorf("兜底打开路径 = %q, 期望 /app/noapp/", got)
	}
}

// TestNormalizePrefix 前缀归一化：补前导斜杠、去尾斜杠
func TestNormalizePrefix(t *testing.T) {
	cases := map[string]string{
		"/app/fnchat":   "/app/fnchat",
		"/app/fnchat/":  "/app/fnchat",
		"app/fnchat":    "/app/fnchat",
		"/app/miyin///": "/app/miyin",
		"":              "",
	}
	for in, want := range cases {
		if got := normalizePrefix(in); got != want {
			t.Errorf("normalizePrefix(%q) = %q, 期望 %q", in, got, want)
		}
	}
}

// TestPrefixFromURL 从 url 推导前缀：去查询串与尾斜杠
func TestPrefixFromURL(t *testing.T) {
	checks := []struct{ in, want string }{
		{"/app/fnchat/", "/app/fnchat"},
		{"/app/file-collector/gateway", "/app/file-collector/gateway"},
		{"/app/fygo-browser/browser.html?v=1", "/app/fygo-browser/browser.html"},
		{"/app/x#frag", "/app/x"},
	}
	for _, c := range checks {
		if got := prefixFromURL(c.in); got != c.want {
			t.Errorf("prefixFromURL(%q) = %q, 期望 %q", c.in, got, c.want)
		}
	}
}

// TestTemplatePlaceholderDetection 覆盖 ui/config 把端口写成模板占位符的情况。
//
// 实例：Lucky 的 ui/config 写 `"port": "${port}"`，表示端口由安装向导的
// port 字段决定，而非字面量。若直接把 "${port}" 当端口使用，
// 应用会拿着这个字符串去监听并启动失败。
func TestTemplatePlaceholderDetection(t *testing.T) {
	placeholder := []string{"${port}", "$port", "{port}", "{{port}}", "%s", "%d", "${1}", " ${port} "}
	for _, s := range placeholder {
		if !isTemplatePlaceholder(s) {
			t.Errorf("%q 应被判为占位符", s)
		}
	}
	real := []string{"16601", "8080", "80", "", "20128", "0"}
	for _, s := range real {
		if isTemplatePlaceholder(s) {
			t.Errorf("%q 不应被判为占位符", s)
		}
	}
}

// TestReadPortRejectsPlaceholder 确认占位符不会被当作端口返回
func TestReadPortRejectsPlaceholder(t *testing.T) {
	dir := t.TempDir()
	appDir := filepath.Join(dir, "apps", "lucky")
	targetDir := filepath.Join(appDir, "target")
	if err := os.MkdirAll(filepath.Join(targetDir, "ui"), 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := `{
	  ".url": {
	    "Lucky.Application": {
	      "title": "Lucky",
	      "type": "url",
	      "port": "${port}",
	      "url": "${safety}"
	    }
	  }
	}`
	if err := os.WriteFile(filepath.Join(targetDir, "ui", "config"), []byte(cfg), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := readPortFromUIConfig(targetDir, appDir); got != "" {
		t.Errorf("占位符端口应返回空字符串，实际 %q", got)
	}
}

// TestResolveLaunchMode 验证 ui/config 的 type 决定打开方式。
// 飞牛文档：iframe → 桌面窗口内打开；url → 浏览器标签页。
func TestResolveLaunchMode(t *testing.T) {
	cases := []struct {
		name string
		cfg  string
		want LaunchMode
	}{
		{"iframe → 子窗口", `{".url":{"a.x":{"type":"iframe","url":"/app/a"}}}`, LaunchIframe},
		{"url → 新标签页", `{".url":{"a.x":{"type":"url","url":"/app/a"}}}`, LaunchTab},
		{"大写 IFrame 也识别", `{".url":{"a.x":{"type":"IFRAME"}}}`, LaunchIframe},
		{"无 type 默认新标签页", `{".url":{"a.x":{"url":"/app/a"}}}`, LaunchTab},
		{"非标准 JSON 兜底", `{".url":{"a.x":{"type":"iframe",},},}`, LaunchIframe},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dir := t.TempDir()
			appDir := filepath.Join(dir, "app")
			target := filepath.Join(appDir, "target")
			if err := os.MkdirAll(filepath.Join(target, "ui"), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(target, "ui", "config"), []byte(c.cfg), 0o644); err != nil {
				t.Fatal(err)
			}
			if got := ResolveLaunchMode(target, appDir); got != c.want {
				t.Errorf("ResolveLaunchMode = %q, 期望 %q", got, c.want)
			}
		})
	}
	t.Run("无 ui/config → 新标签页", func(t *testing.T) {
		dir := t.TempDir()
		if got := ResolveLaunchMode(filepath.Join(dir, "t"), dir); got != LaunchTab {
			t.Errorf("= %q, 期望 %q", got, LaunchTab)
		}
	})
}

// TestParseUIConfigProtocol 验证 protocol 与可见性字段被解析
func TestParseUIConfigProtocol(t *testing.T) {
	cfg := `{".url":{"a.main":{
      "title":"My App","type":"iframe","protocol":"https",
      "url":"/app/a","allUsers":true,"noDisplay":false}}}`
	entries := ParseUIConfig([]byte(cfg))
	if len(entries) != 1 {
		t.Fatalf("条目数 = %d", len(entries))
	}
	e := entries[0]
	if e.Type != "iframe" || e.Protocol != "https" || e.Title != "My App" {
		t.Errorf("解析结果: %+v", e)
	}
	if !e.AllUsers || e.NoDisplay {
		t.Errorf("可见性字段: allUsers=%v noDisplay=%v", e.AllUsers, e.NoDisplay)
	}
}
