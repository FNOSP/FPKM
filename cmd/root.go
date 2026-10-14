package cmd

import (
	"fmt"
	"os"

	"fpkm/internal/config"
	"fpkm/internal/logger"

	"github.com/spf13/cobra"
)

var verboseMode bool

var rootCmd = &cobra.Command{
	Use:   "fpkm",
	Short: "飞牛面板 - 非飞牛系统上的 FPK 应用管理器",
	Long: `飞牛面板 (FPKM) 是一个在非飞牛 fnOS 系统上运行 .fpk 应用包的工具。
无需 Root 权限，支持在 Windows、macOS、Linux 上安装、启动、停止、
状态查看和卸载飞牛应用。

完整生命周期管理：install / start / stop / status / upgrade / uninstall`,
	PersistentPreRun: func(cmd *cobra.Command, args []string) {
		// 初始化日志（子命令实际使用前才初始化）
		if cmd.Name() == "completion" || cmd.Name() == "help" {
			return
		}
		cfg := config.GetConfig()
		level := logger.LevelInfo
		if verboseMode {
			level = logger.LevelDebug
		}
		logger.Init(cfg.LogDir, level)
	},
}

func init() {
	rootCmd.PersistentFlags().BoolVarP(&verboseMode, "verbose", "v", false, "详细日志输出（显示所有执行的命令）")
}

// Execute 执行根命令
func Execute() {
	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
