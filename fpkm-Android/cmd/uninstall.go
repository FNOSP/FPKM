package cmd

import (
	"fpkm/internal/app"
	"fpkm/internal/config"
	"fpkm/internal/logger"

	"github.com/spf13/cobra"
)

var uninstallCmd = &cobra.Command{
	Use:   "uninstall <应用名>",
	Short: "卸载应用",
	Long: `卸载已安装的飞牛应用。

会先停止运行中的进程，然后删除应用目录及所有数据。`,
	Args: cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		appname := args[0]
		cfg := config.GetConfig()
		mgr := app.NewManager(cfg)

		if err := mgr.Uninstall(appname, nil); err != nil {
			logger.Fatal("卸载失败: %v", err)
		}
	},
}

func init() {
	rootCmd.AddCommand(uninstallCmd)
}
