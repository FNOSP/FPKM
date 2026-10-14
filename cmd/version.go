package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
)

const version = "1.0.0"

var versionCmd = &cobra.Command{
	Use:   "version",
	Short: "显示版本信息",
	Run: func(cmd *cobra.Command, args []string) {
		fmt.Printf("飞牛面板 (fpkm) v%s\n", version)
		fmt.Println("非飞牛系统上的 FPK 应用管理器")
		fmt.Println("https://github.com/fpkm/fpkm")
	},
}

func init() {
	rootCmd.AddCommand(versionCmd)
}
