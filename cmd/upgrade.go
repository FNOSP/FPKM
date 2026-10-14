package cmd

import (
	"fpkm/internal/app"
	"fpkm/internal/config"
	"fpkm/internal/logger"

	"github.com/spf13/cobra"
)

var upgradeSet []string

var upgradeCmd = &cobra.Command{
	Use:   "upgrade <fpk文件路径>",
	Short: "升级应用",
	Long: `升级已安装的飞牛应用到新版本。

升级流程：
  1. 解压新版本获取信息
  2. 停止当前运行的应用
  3. 备份旧版本
  4. 安装新版本
  5. 迁移配置和数据 (etc/var/home)
  6. 清理备份

升级失败时自动恢复旧版本。`,
	Args: cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		fpkPath := args[0]
		cfg := config.GetConfig()
		mgr := app.NewManager(cfg)

		if err := mgr.Upgrade(fpkPath, upgradeSet); err != nil {
			logger.Fatal("升级失败: %v", err)
		}
	},
}

func init() {
	rootCmd.AddCommand(upgradeCmd)
	upgradeCmd.Flags().StringArrayVarP(&upgradeSet, "set", "s", []string{}, "设置向导参数 (key=value)")
}
