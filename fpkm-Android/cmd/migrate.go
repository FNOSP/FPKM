package cmd

import (
	"fmt"

	"github.com/spf13/cobra"

	"fpkm/internal/config"
	"fpkm/internal/logger"
)

var migrateData bool

var migrateCmd = &cobra.Command{
	Use:   "migrate-data",
	Short: "把旧位置的数据迁移到程序旁的 data 目录",
	Long: `把旧版本存储在 $HOME/.local/fpk（Windows 为 %USERPROFILE%\.fpk）的数据
迁移到「可执行文件所在目录/data」。

迁移策略：
  - 目标已存在的条目会跳过，不会覆盖新数据
  - 迁移后保留源目录，确认无误后可手动删除
  - 用复制而非移动，支持跨文件系统

建议先停止 fpkm 服务再执行，避免迁移过程中文件被占用。`,
	Run: func(cmd *cobra.Command, args []string) {
		cfg := config.GetConfig()
		logger.Init(cfg.LogDir, logger.LevelInfo)

		fmt.Printf("旧数据目录: %s\n", config.LegacyBaseDirForDisplay())
		fmt.Printf("新数据目录: %s\n\n", cfg.BaseDir)

		res, err := config.MigrateFromLegacy()
		if err != nil {
			logger.Fatal("迁移失败: %v", err)
		}
		if res.NoSource {
			fmt.Println("旧目录不存在或与新目录相同，无需迁移。")
			return
		}

		if len(res.Moved) > 0 {
			fmt.Printf("已迁移 %d 项:\n", len(res.Moved))
			for _, n := range res.Moved {
				fmt.Printf("  ✓ %s\n", n)
			}
		}
		if len(res.Skipped) > 0 {
			fmt.Printf("跳过 %d 项（目标已存在，未覆盖）:\n", len(res.Skipped))
			for _, n := range res.Skipped {
				fmt.Printf("  - %s\n", n)
			}
		}
		if len(res.Failed) > 0 {
			fmt.Printf("失败 %d 项:\n", len(res.Failed))
			for _, n := range res.Failed {
				fmt.Printf("  ✗ %s\n", n)
			}
			fmt.Printf("\n源目录已保留: %s\n", res.From)
			return
		}
		fmt.Printf("\n源目录已保留，确认无误后可手动删除: %s\n", res.From)
	},
}

func init() {
	migrateCmd.Flags().BoolVar(&migrateData, "dry-run", false, "只检查不执行（保留参数位）")
	rootCmd.AddCommand(migrateCmd)
}
