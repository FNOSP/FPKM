package cmd

import (
	"fmt"

	"fpkm/internal/app"
	"fpkm/internal/config"
	"fpkm/internal/logger"

	"github.com/spf13/cobra"
)

var statusCmd = &cobra.Command{
	Use:   "status <应用名>",
	Short: "查看应用状态",
	Long: `查看已安装应用的运行状态，包括：
  - 运行状态 (Running / Stopped)
  - 进程 PID
  - 服务端口
  - 应用类型
  - 版本信息`,
	Args: cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		appname := args[0]
		cfg := config.GetConfig()
		mgr := app.NewManager(cfg)

		status, err := mgr.Status(appname)
		if err != nil {
			logger.Fatal("查询状态失败: %v", err)
		}

		fmt.Println("┌─────────────────────────────────────┐")
		fmt.Printf("│ 应用名称: %-26s │\n", status.Name)
		fmt.Printf("│ 版本:     %-26s │\n", status.Version)
		if status.Running {
			fmt.Printf("│ 状态:     %-26s │\n", "Running (PID: "+fmt.Sprintf("%d", status.PID)+")")
		} else {
			fmt.Printf("│ 状态:     %-26s │\n", "Stopped")
		}
		if status.Port != "" {
			fmt.Printf("│ 端口:     %-26s │\n", status.Port)
		}
		if status.EntryType != "" {
			fmt.Printf("│ 类型:     %-26s │\n", status.EntryType)
		}
		fmt.Println("└─────────────────────────────────────┘")
	},
}

func init() {
	rootCmd.AddCommand(statusCmd)
}
