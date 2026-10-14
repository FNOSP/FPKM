package cmd

import (
	"fpkm/internal/app"
	"fpkm/internal/config"
	"fpkm/internal/logger"

	"github.com/spf13/cobra"
)

var startCmd = &cobra.Command{
	Use:   "start <应用名>",
	Short: "启动应用",
	Long: `启动已安装的飞牛应用。

自动探测应用入口类型（Node.js / 二进制 / Python / 静态文件），
设置环境变量并启动子进程。

端口冲突时自动切换到可用端口。`,
	Args: cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		appname := args[0]
		cfg := config.GetConfig()
		mgr := app.NewManager(cfg)

		if err := mgr.Start(appname); err != nil {
			logger.Fatal("启动失败: %v", err)
		}
	},
}

func init() {
	rootCmd.AddCommand(startCmd)
}
