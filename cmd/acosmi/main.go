// =============================================================================
// acosmi —— Acosmi 智能体一键接入 CLI
//
// 方案出处: docs/api-open/subscription-entitlement-api-agent-integration-plan-2026-08-13.md §7
//
// 一条命令完成: OAuth 登录 → 签发最小权限的会员委托凭证 → 写入目标智能体配置 → 验证 → 可逆卸载。
//
//	acosmi agent add codex --login --billing subscription --model auto
//
// 宿主选型 (§0 B1 定死): 本 CLI 落在本仓 Go SDK 下 (acosmi-sdk-go/cmd/acosmi), 复用 SDK 的
// OAuth / TokenStore / Device Flow 能力, 先例是同目录的 crabclawskill。
//
// 三条不可动摇的行为约束:
//   - 密钥明文绝不进 stdout 之外的任何地方 (§红线 9): 不落日志、不进命令行参数、不写 manifest。
//     它只在写入目标配置文件的那一刻存在于内存里。
//   - 所有配置写入必须 备份 → 临时文件 → 原子 rename, 且保留目标文件里我们不认识的字段。
//     用户的配置是他们的, 我们只补自己那几行。
//   - remove 只回滚**自己**加的补丁与自己签发的凭证, 绝不动用户后来的修改。
// =============================================================================

package main

import (
	"fmt"
	"os"

	acosmi "github.com/acosmi/acosmi-sdk-go"
	"github.com/spf13/cobra"
)

var (
	version   = "dev"
	buildTime = "unknown"
)

var (
	flagServer string
	flagJSON   bool
	client     *acosmi.Client
)

// needsClient 纯本地命令不必构造 SDK client (也就不必读 token 文件)。
func needsClient(cmd *cobra.Command) bool {
	switch cmd.Name() {
	case "version", "completion", "help":
		return false
	}
	// agent list / doctor 读的是本地 manifest 与目标配置, 不需要服务端。
	if cmd.Parent() != nil && cmd.Parent().Name() == "agent" {
		switch cmd.Name() {
		case "list":
			return false
		}
	}
	return true
}

var rootCmd = &cobra.Command{
	Use:   "acosmi",
	Short: "Acosmi 智能体接入 CLI",
	Long: "acosmi —— 把 Acosmi 的模型能力接进你的智能体\n\n" +
		"典型用法:\n" +
		"  acosmi auth login --device\n" +
		"  acosmi agent add codex --login --billing subscription --model auto\n" +
		"  acosmi agent doctor codex\n" +
		"  acosmi agent remove codex",
	SilenceUsage: true,
	PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
		if !needsClient(cmd) {
			return nil
		}
		cfg := loadCLIConfig()
		if flagServer != "" {
			cfg.ServerURL = flagServer
		}
		c, err := acosmi.NewClient(acosmi.Config{ServerURL: cfg.ServerURL})
		if err != nil {
			return fmt.Errorf("create client: %w", err)
		}
		client = c
		return nil
	},
}

func init() {
	rootCmd.PersistentFlags().StringVar(&flagServer, "server", "", "服务器地址 (覆盖配置; 默认 https://acosmi.com)")
	rootCmd.PersistentFlags().BoolVar(&flagJSON, "json", false, "JSON 输出 (方便脚本集成)")
}

func requireAuth() error {
	if client == nil || !client.IsAuthorized() {
		return fmt.Errorf("未登录。请先运行: acosmi auth login")
	}
	return nil
}

func main() {
	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, "错误:", err)
		os.Exit(1)
	}
}
