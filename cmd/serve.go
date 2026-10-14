package cmd

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"
)

var serveDir string
var servePort int

// serveCmd 内置静态文件服务器命令
// 当应用入口为静态文件时，fpkm 自动启动此服务
var serveCmd = &cobra.Command{
	Use:    "serve",
	Short:  "内置静态文件服务器（内部使用）",
	Long:   `为静态文件类应用提供 HTTP 服务。通常由 fpkm start 自动调用。`,
	Hidden: true,
	Run: func(cmd *cobra.Command, args []string) {
		absDir, err := filepath.Abs(serveDir)
		if err != nil {
			fmt.Fprintf(os.Stderr, "路径错误: %v\n", err)
			os.Exit(1)
		}

		fmt.Printf("静态文件服务启动\n目录: %s\n端口: %d\n", absDir, servePort)

		fs := http.FileServer(http.Dir(absDir))
		http.Handle("/", fs)

		addr := fmt.Sprintf(":%d", servePort)
		if err := http.ListenAndServe(addr, nil); err != nil {
			fmt.Fprintf(os.Stderr, "服务器启动失败: %v\n", err)
			os.Exit(1)
		}
	},
}

func init() {
	rootCmd.AddCommand(serveCmd)
	serveCmd.Flags().StringVar(&serveDir, "dir", ".", "静态文件目录")
	serveCmd.Flags().IntVar(&servePort, "port", 8080, "监听端口")
}
