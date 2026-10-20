package cmd

import (
	"fmt"
	"runtime"

	"github.com/spf13/cobra"
)

// version 在编译时通过 -ldflags 注入，默认值用于本地开发构建。
//
//	go build -ldflags "-X fpkm/cmd.version=1.0.1"
//
// 这样发版时无需改源码，构建脚本与 CI 都能直接指定版本号。
var version = "dev"

// buildTime 同样可注入，便于排查用户实际运行的构建。
var buildTime = ""

var versionCmd = &cobra.Command{
	Use:   "version",
	Short: "显示版本信息",
	Run: func(cmd *cobra.Command, args []string) {
		fmt.Printf("FPKM (fpkm) v%s\n", version)
		fmt.Println("在非飞牛系统上运行 .fpk 离线应用包")
		fmt.Printf("平台: %s/%s\n", runtime.GOOS, runtime.GOARCH)
		if buildTime != "" {
			fmt.Printf("构建时间: %s\n", buildTime)
		}
		fmt.Println("https://github.com/FNOSP/FPKM")
	},
}

func init() {
	rootCmd.AddCommand(versionCmd)
}
