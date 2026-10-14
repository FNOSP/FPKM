package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestParseWizardRealFiles 用真实 FPK 包里的向导文件验证解析。
// 这些文案取自实际应用，覆盖了工程中最容易踩的几种写法。
func TestParseWizardRealFiles(t *testing.T) {
	// Lucky 的安装向导：tips + text，带必须保持默认值的 pattern
	luckyInstall := `[
    {
        "stepTitle": "欢迎安装Lucky",
        "items": [
            { "type": "tips", "helpText": "欢迎使用Lucky全能版" }
        ]
    },
    {
        "stepTitle": "默认配置设置",
        "items": [
            {
                "type": "text",
                "field": "port",
                "label": "端口号设置",
                "initValue": "16601",
                "placeholder": "请保持默认值16601",
                "rules": [
                    { "required": true, "message": "端口号不能为空" },
                    { "pattern": "^16601$", "message": "请使用默认值'16601'完成安装" }
                ]
            },
            {
                "type": "text",
                "field": "safety",
                "label": "安全路径设置",
                "initValue": "/",
                "rules": [
                    { "required": true, "message": "访问路径不能为空" },
                    { "pattern": "^\\/$", "message": "请使用默认值'/'完成安装" }
                ]
            }
        ]
    }
]`

	// file-collector：text + password + 数字 pattern
	fileCollectorInstall := `[
    {
        "stepTitle": "管理员与端口",
        "items": [
            {
                "type": "text",
                "field": "wizard_admin_user",
                "label": "管理员用户名",
                "initValue": "admin",
                "rules": [
                    { "required": true, "message": "请输入管理员用户名" },
                    { "min": 2, "max": 20, "message": "用户名长度应在2-20个字符之间" }
                ]
            },
            {
                "type": "password",
                "field": "wizard_admin_pass",
                "label": "管理员密码",
                "rules": [
                    { "required": true, "message": "请输入管理员密码" },
                    { "min": 6, "message": "密码长度不能少于6位" }
                ]
            },
            {
                "type": "text",
                "field": "wizard_app_port",
                "label": "监听端口",
                "initValue": "5557",
                "rules": [
                    { "required": true, "message": "请输入监听端口" },
                    { "pattern": "^[0-9]+$", "message": "端口号必须是数字" }
                ]
            }
        ]
    }
]`

	// fnchat / Lucky 的卸载向导：radio 保留或删除数据
	uninstallRadio := `[{
  "stepTitle": "卸载应用",
  "items": [
    { "type": "tips", "helpText": "保留或删除应用设置。" },
    {
      "type": "radio",
      "field": "wizard_delete_data",
      "label": "仅卸载，保留现有配置以备将来重新安装。",
      "initValue": "false",
      "options": [
        { "label": "保留现有用户配置", "value": "false" },
        { "label": "清除所有应用数据文件（不可恢复）！！！", "value": "true" }
      ]
    }
  ]
}]`

	// 含 HTML 的文案（file-collector 的富文本提示）
	htmlTips := `[
  {
    "stepTitle": "安装向导",
    "items": [
      {
        "type": "tips",
        "helpText": "<strong>欢迎使用文件收集器</strong><br><br>• 创建收集链接<br>• 大文件断点续传"
      }
    ]
  }
]`

	cases := []struct {
		name      string
		data      string
		wantSteps int
		check     func(t *testing.T, steps []WizardStep)
	}{
		{
			name:      "Lucky 安装向导",
			data:      luckyInstall,
			wantSteps: 2,
			check: func(t *testing.T, steps []WizardStep) {
				if steps[0].StepTitle != "欢迎安装Lucky" {
					t.Errorf("步骤标题 = %q", steps[0].StepTitle)
				}
				if len(steps[0].Items) != 1 || steps[0].Items[0].Type != "tips" {
					t.Errorf("第一步应为 1 个 tips")
				}
				port := steps[1].Items[0]
				if port.Field != "port" || port.InitValue != "16601" {
					t.Errorf("port 字段解析错误: %+v", port)
				}
				if len(port.Rules) != 2 || port.Rules[1].Pattern != "^16601$" {
					t.Errorf("port 规则解析错误: %+v", port.Rules)
				}
				// 默认值必须能被正确收集
				def := WizardDefaults(steps)
				if def["port"] != "16601" || def["safety"] != "/" {
					t.Errorf("默认值 = %+v", def)
				}
			},
		},
		{
			name:      "file-collector 管理员与端口",
			data:      fileCollectorInstall,
			wantSteps: 1,
			check: func(t *testing.T, steps []WizardStep) {
				items := steps[0].Items
				if len(items) != 3 {
					t.Fatalf("字段数 = %d, 期望 3", len(items))
				}
				if items[1].Type != "password" {
					t.Errorf("密码字段类型 = %q, 期望 password", items[1].Type)
				}
				if items[0].Rules[1].Min == nil || *items[0].Rules[1].Min != 2 {
					t.Errorf("min 规则未解析: %+v", items[0].Rules[1])
				}
			},
		},
		{
			name:      "卸载 radio",
			data:      uninstallRadio,
			wantSteps: 1,
			check: func(t *testing.T, steps []WizardStep) {
				radio := steps[0].Items[1]
				if radio.Type != "radio" {
					t.Fatalf("类型 = %q, 期望 radio", radio.Type)
				}
				if len(radio.Options) != 2 {
					t.Fatalf("选项数 = %d, 期望 2", len(radio.Options))
				}
				if radio.Options[1].Value != "true" {
					t.Errorf("第二个选项值 = %q", radio.Options[1].Value)
				}
				if radio.InitValue != "false" {
					t.Errorf("默认值 = %q, 期望 false", radio.InitValue)
				}
			},
		},
		{
			name:      "含 HTML 的 tips 原样保留",
			data:      htmlTips,
			wantSteps: 1,
			check: func(t *testing.T, steps []WizardStep) {
				h := steps[0].Items[0].HelpText
				if h == "" || !strings.Contains(h, "<strong>") || !strings.Contains(h, "<br>") {
					t.Errorf("HTML 文案未保留: %q", h)
				}
			},
		},
		{
			name:      "空向导",
			data:      `[]`,
			wantSteps: 0,
			check:     func(t *testing.T, steps []WizardStep) {},
		},
		{
			name: "尾随逗号（非标准 JSON）",
			data: `[
              { "stepTitle": "A", "items": [ { "type": "tips", "helpText": "x" }, ] },
            ]`,
			wantSteps: 1,
			check:     func(t *testing.T, steps []WizardStep) {},
		},
		{
			name: "未知类型降级为 tips",
			data: `[{"stepTitle":"A","items":[
                    {"type":"future-widget","helpText":"未来的控件"},
                    {"type":"tips","helpText":"ok"}]}]`,
			wantSteps: 1,
			check: func(t *testing.T, steps []WizardStep) {
				if len(steps[0].Items) != 2 {
					t.Fatalf("字段数 = %d", len(steps[0].Items))
				}
				if steps[0].Items[0].Type != "tips" {
					t.Errorf("未知类型应降级为 tips, 实际 %q", steps[0].Items[0].Type)
				}
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			steps, err := ParseWizard([]byte(tc.data))
			if err != nil {
				t.Fatalf("解析失败: %v", err)
			}
			if len(steps) != tc.wantSteps {
				t.Fatalf("步骤数 = %d, 期望 %d", len(steps), tc.wantSteps)
			}
			tc.check(t, steps)
		})
	}
}

// TestValidateWizardValues 覆盖真实向导里的校验规则
func TestValidateWizardValues(t *testing.T) {
	steps, err := ParseWizard([]byte(`[
      {
        "stepTitle": "管理员与端口",
        "items": [
          { "type": "text", "field": "user", "rules": [
              { "required": true, "message": "请输入用户名" },
              { "min": 2, "max": 5, "message": "长度2-5" } ] },
          { "type": "text", "field": "port", "rules": [
              { "required": true, "message": "请输入端口" },
              { "pattern": "^[0-9]+$", "message": "端口必须是数字" } ] },
          { "type": "text", "field": "keep", "initValue": "16601", "rules": [
              { "pattern": "^16601$", "message": "必须保持默认值" } ] }
        ]
      }
    ]`))
	if err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		name    string
		values  map[string]string
		wantErr map[string]string // field -> 期望报错（空表示期望通过）
	}{
		{
			name:   "全部合法",
			values: map[string]string{"user": "admin", "port": "5557", "keep": "16601"},
		},
		{
			name:    "必填为空",
			values:  map[string]string{"user": "", "port": "80", "keep": "16601"},
			wantErr: map[string]string{"user": "请输入用户名"},
		},
		{
			name:    "长度不足",
			values:  map[string]string{"user": "a", "port": "80", "keep": "16601"},
			wantErr: map[string]string{"user": "长度2-5"},
		},
		{
			name:    "长度超限",
			values:  map[string]string{"user": "abcdefgh", "port": "80", "keep": "16601"},
			wantErr: map[string]string{"user": "长度2-5"},
		},
		{
			name:    "正则不匹配",
			values:  map[string]string{"user": "admin", "port": "abc", "keep": "16601"},
			wantErr: map[string]string{"port": "端口必须是数字"},
		},
		{
			name:    "必须保持默认值",
			values:  map[string]string{"user": "admin", "port": "80", "keep": "9999"},
			wantErr: map[string]string{"keep": "必须保持默认值"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			errs := ValidateWizardValues(steps, tc.values)
			if len(tc.wantErr) == 0 {
				if len(errs) != 0 {
					t.Fatalf("期望通过，实际报错: %v", errs)
				}
				return
			}
			for field, want := range tc.wantErr {
				got, ok := errs[field]
				if !ok {
					t.Fatalf("字段 %s 期望报错 %q，实际无错误 (全部: %v)", field, want, errs)
				}
				if got != want {
					t.Errorf("字段 %s 报错 = %q, 期望 %q", field, got, want)
				}
			}
		})
	}
}

// TestResolveWizardI18n 验证 ${section.key} 占位符替换
func TestResolveWizardI18n(t *testing.T) {
	dir := t.TempDir()
	appDir := filepath.Join(dir, "app")

	// i18n 目录（EasyTier 风格）
	if err := os.MkdirAll(filepath.Join(appDir, "i18n"), 0o755); err != nil {
		t.Fatal(err)
	}
	ini := `[install]
stepTitle="欢迎安装"
tips1.helpText="这是第一条提示"

[uninstall]
stepTitle="卸载应用"
`
	if err := os.WriteFile(filepath.Join(appDir, "i18n", "zh-CN"), []byte(ini), 0o644); err != nil {
		t.Fatal(err)
	}

	steps, err := ParseWizard([]byte(`[
      { "stepTitle": "${install.stepTitle}", "items": [
          { "type": "tips", "helpText": "${install.tips1.helpText}" },
          { "type": "radio", "field": "wizard_data_action",
            "label": "${uninstall.stepTitle}",
            "options": [ { "label": "${install.stepTitle}", "value": "keep" } ],
            "rules": [ { "required": true, "message": "${install.stepTitle}" } ] }
      ] } ]`))
	if err != nil {
		t.Fatal(err)
	}

	steps = ResolveWizardI18n(appDir, WizardInstall, steps)

	if steps[0].StepTitle != "欢迎安装" {
		t.Errorf("stepTitle = %q, 期望 欢迎安装", steps[0].StepTitle)
	}
	if steps[0].Items[0].HelpText != "这是第一条提示" {
		t.Errorf("helpText = %q", steps[0].Items[0].HelpText)
	}
	radio := steps[0].Items[1]
	if radio.Label != "卸载应用" {
		t.Errorf("label = %q", radio.Label)
	}
	if radio.Options[0].Label != "欢迎安装" {
		t.Errorf("option label = %q", radio.Options[0].Label)
	}
	if radio.Rules[0].Message != "欢迎安装" {
		t.Errorf("rule message = %q", radio.Rules[0].Message)
	}
}

// TestResolveWizardI18nMissingKey 未命中的占位符应原样保留，便于排查
func TestResolveWizardI18nMissingKey(t *testing.T) {
	dir := t.TempDir()
	appDir := filepath.Join(dir, "app")
	_ = os.MkdirAll(filepath.Join(appDir, "i18n"), 0o755)
	_ = os.WriteFile(filepath.Join(appDir, "i18n", "zh-CN"), []byte("[install]\nstepTitle=\"标题\"\n"), 0o644)

	steps, _ := ParseWizard([]byte(`[{"stepTitle":"${install.nope}","items":[{"type":"tips","helpText":"x"}]}]`))
	steps = ResolveWizardI18n(appDir, WizardInstall, steps)

	if steps[0].StepTitle != "${install.nope}" {
		t.Errorf("未命中占位符应保留原样，实际 %q", steps[0].StepTitle)
	}
}

// TestWizardDefaultsFallback 无 initValue 时按类型给出兜底默认值
func TestWizardDefaultsFallback(t *testing.T) {
	steps, _ := ParseWizard([]byte(`[{"stepTitle":"s","items":[
        {"type":"radio","field":"r","options":[{"label":"A","value":"a"}]},
        {"type":"switch","field":"sw"},
        {"type":"text","field":"t"}]}]`))

	def := WizardDefaults(steps)
	if def["r"] != "a" {
		t.Errorf("radio 默认值 = %q, 期望首选项 a", def["r"])
	}
	if def["sw"] != "false" {
		t.Errorf("switch 默认值 = %q, 期望 false", def["sw"])
	}
	if _, ok := def["t"]; !ok {
		t.Errorf("text 字段应存在（值可为空）")
	}
}
