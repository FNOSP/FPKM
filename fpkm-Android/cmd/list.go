package cmd

import (
	"fmt"
	"os"
	"text/tabwriter"

	"fpkm/internal/app"
	"fpkm/internal/config"
	"fpkm/internal/logger"

	"github.com/spf13/cobra"
)

var listCmd = &cobra.Command{
	Use:   "list",
	Short: "列出已安装应用",
	Long:  `列出所有已安装的飞牛应用及其基本信息。`,
	Args:  cobra.NoArgs,
	Run: func(cmd *cobra.Command, args []string) {
		cfg := config.GetConfig()
		mgr := app.NewManager(cfg)

		apps, err := mgr.List()
		if err != nil {
			logger.Fatal("获取应用列表失败: %v", err)
		}

		if len(apps) == 0 {
			fmt.Println("暂无已安装的应用")
			fmt.Printf("使用 fpkm install <fpk文件路径> 安装应用\n")
			return
		}

		w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
		fmt.Fprintln(w, "名称\t版本\t显示名\t类型\t状态")
		fmt.Fprintln(w, "───\t───\t───\t───\t───")

		for _, a := range apps {
			// 获取运行状态
			status, _ := mgr.Status(a.Name)
			state := "Stopped"
			if status != nil && status.Running {
				state = "Running"
			}

			appType := "Native"
			if a.IsDocker {
				appType = "Docker"
			}
			displayName := a.DisplayName
			if displayName == "" {
				displayName = a.Name
			}

			fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n", a.Name, a.Version, displayName, appType, state)
		}
		w.Flush()

		fmt.Printf("\n共 %d 个应用\n", len(apps))
	},
}

func init() {
	rootCmd.AddCommand(listCmd)
}
