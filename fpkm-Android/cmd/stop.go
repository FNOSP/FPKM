package cmd

import (
	"fpkm/internal/app"
	"fpkm/internal/config"
	"fpkm/internal/logger"

	"github.com/spf13/cobra"
)

var stopCmd = &cobra.Command{
	Use:   "stop <应用名>",
	Short: "停止应用",
	Long: `停止正在运行的飞牛应用。

先尝试优雅终止（SIGTERM），超时后强制杀死。`,
	Args: cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		appname := args[0]
		cfg := config.GetConfig()
		mgr := app.NewManager(cfg)

		if err := mgr.Stop(appname); err != nil {
			logger.Fatal("停止失败: %v", err)
		}
	},
}

func init() {
	rootCmd.AddCommand(stopCmd)
}
