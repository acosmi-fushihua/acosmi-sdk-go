package main

import (
	"fmt"

	"github.com/spf13/cobra"
)

func init() {
	rootCmd.AddCommand(versionCmd, targetsCmd)
}

var versionCmd = &cobra.Command{
	Use:   "version",
	Short: "显示版本",
	Run: func(cmd *cobra.Command, args []string) {
		fmt.Printf("acosmi %s (built %s)\n", version, buildTime)
	},
}

// targetsCmd 列出支持的目标 —— 让 "acosmi agent add 什么" 这个问题有个自解释的答案,
// 而不是逼用户去读文档或猜。
var targetsCmd = &cobra.Command{
	Use:   "targets",
	Short: "列出支持的智能体目标",
	Run: func(cmd *cobra.Command, args []string) {
		fmt.Printf("%-20s %-12s %-34s %s\n", "目标", "协议", "配置文件", "本机检测")
		for _, id := range sortedAdapterIDs() {
			a := adapters()[id]
			detected, path := a.Detect()
			mark := "未检测到"
			if detected {
				mark = "已检测到"
			}
			fmt.Printf("%-20s %-12s %-34s %s\n", a.ID(), a.Protocol(), path, mark)
		}
	},
}

func sortedAdapterIDs() []string {
	all := adapters()
	ids := make([]string, 0, len(all))
	for id := range all {
		ids = append(ids, id)
	}
	// 与 lookupAdapter 的错误提示保持同序, 免得两处顺序不一致让人以为是两套清单。
	for i := 0; i < len(ids); i++ {
		for j := i + 1; j < len(ids); j++ {
			if ids[j] < ids[i] {
				ids[i], ids[j] = ids[j], ids[i]
			}
		}
	}
	return ids
}
