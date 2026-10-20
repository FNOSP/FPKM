package cmd

import (
	"fpkm/internal/app"
	"fpkm/internal/config"
	"fpkm/internal/logger"

	"github.com/spf13/cobra"
)

var installSet []string

var installCmd = &cobra.Command{
	Use:   "install <fpk文件路径>",
	Short: "安装 FPK 应用包",
	Long: `安装飞牛 .fpk 应用包到用户目录。

会自动完成：
  1. 校验包完整性
  2. 解压到 ~/.local/fpk/apps/{appname}/
  3. 生成环境变量映射 (.env)
  4. 处理安装向导默认值

使用 --set 传入向导参数：--set wizard_port=8080 --set wizard_username=admin`,
	Args: cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		fpkPath := args[0]
		cfg := config.GetConfig()
		mgr := app.NewManager(cfg)

		if err := mgr.Install(fpkPath, installSet); err != nil {
			logger.Fatal("安装失败: %v", err)
		}
	},
}

func init() {
	rootCmd.AddCommand(installCmd)
	installCmd.Flags().StringArrayVarP(&installSet, "set", "s", []string{}, "设置向导参数 (key=value)")
}
