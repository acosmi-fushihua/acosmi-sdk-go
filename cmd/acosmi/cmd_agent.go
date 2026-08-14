package main

// =============================================================================
// acosmi agent —— 一键接入 / 列出 / 体检 / 轮换 / 卸载
//
// `agent add` 的完整内部流程 (§7.2):
//   1. 解析目标适配器, 检测本机是否装了它、配置在哪
//   2. (--login) 未登录则先完成 OAuth
//   3. 选模型 (--model auto 取账号可用的第一个)
//   4. 生成变更计划; --dry-run 到此为止
//   5. 签发一枚**按目标隔离**的会员委托凭证 (scope 最小化, 有效期受服务端硬上限约束)
//   6. 备份 → 原子合并写入目标配置 (保留未知字段)
//   7. 写 CLI-owned manifest (记凭证 id 与补丁, **不记明文**)
//   8. 验证: /v1/models 预检 + 一次最小推理 (--no-verify 可跳过第二步)
//
// 失败即回滚: 任何一步失败都会把已经做过的动作按逆序撤销 —— 配置补丁撤回、凭证吊销。
// 半接入状态比彻底没接入更难排查, 也更危险 (一枚签出去却没人用的凭证仍能花钱)。
// =============================================================================

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	acosmi "github.com/acosmi/acosmi-sdk-go"
	"github.com/spf13/cobra"
)

var (
	flagAgentLogin     bool
	flagAgentBilling   string
	flagAgentModel     string
	flagAgentDryRun    bool
	flagAgentNoVerify  bool
	flagAgentExpiry    int
	flagAgentUsageDays int
)

func init() {
	agentCmd.AddCommand(agentAddCmd, agentListCmd, agentDoctorCmd, agentRotateCmd, agentRemoveCmd)

	agentAddCmd.Flags().BoolVar(&flagAgentLogin, "login", false, "未登录时先完成 OAuth 授权")
	agentAddCmd.Flags().StringVar(&flagAgentBilling, "billing", "subscription", "计费来源: subscription (会员订阅权益)")
	agentAddCmd.Flags().StringVar(&flagAgentModel, "model", "auto", "模型 id; auto = 自动选账号可用的第一个")
	agentAddCmd.Flags().BoolVar(&flagAgentDryRun, "dry-run", false, "只打印变更计划, 不签发凭证也不改配置")
	agentAddCmd.Flags().BoolVar(&flagAgentNoVerify, "no-verify", false, "跳过最小推理验证 (仍做目录预检)")
	agentAddCmd.Flags().IntVar(&flagAgentExpiry, "expires-days", 90, "凭证有效期天数 (30/60/90; 服务端硬上限 90)")
	agentDoctorCmd.Flags().IntVar(&flagAgentUsageDays, "days", 7, "体检时查看最近几天用量")

	rootCmd.AddCommand(agentCmd)
}

var agentCmd = &cobra.Command{Use: "agent", Short: "把 Acosmi 接进目标智能体"}

// =============================================================================
// add
// =============================================================================

var agentAddCmd = &cobra.Command{
	Use:   "add <目标>",
	Short: "一键接入目标智能体 (签发凭证 + 写配置 + 验证)",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		target := args[0]
		adapter, err := lookupAdapter(target)
		if err != nil {
			return err
		}
		if flagAgentBilling != "subscription" {
			// 第一版只支持会员订阅权益 (§14)。按量 Key 请走开放平台控制台自助签发 ——
			// 那是另一个资金池, 混在同一条命令里只会让用户搞不清自己在花哪笔钱。
			return fmt.Errorf("--billing 目前只支持 subscription (会员订阅权益); 按量付费凭证请在开放平台控制台签发")
		}

		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
		defer cancel()

		if !client.IsAuthorized() {
			if !flagAgentLogin {
				return fmt.Errorf("未登录。加 --login 让本命令顺带完成授权, 或先运行 acosmi auth login")
			}
			fmt.Println("未登录, 先完成授权…")
			if err := client.Login(ctx, "Acosmi CLI", defaultLoginScopes()); err != nil {
				return fmt.Errorf("授权失败: %w", err)
			}
		}

		detected, configPath := adapter.Detect()
		if !detected {
			fmt.Printf("提示: 未在本机检测到 %s, 仍会生成配置到 %s\n", adapter.DisplayName(), configPath)
		}

		modelID, err := resolveModel(ctx, flagAgentModel)
		if err != nil {
			return err
		}

		cfg := loadCLIConfig()
		if flagServer != "" {
			cfg.ServerURL = flagServer
		}
		baseURL := strings.TrimRight(cfg.ServerURL, "/") + "/v1"

		// ── dry-run: 用占位密钥生成计划, 不接触服务端 ──
		if flagAgentDryRun {
			plan, perr := adapter.Plan(AgentSetup{
				BaseURL: baseURL, APIKey: "<将在真正执行时签发>", ModelID: modelID, ServerURL: cfg.ServerURL,
			})
			if perr != nil {
				return perr
			}
			fmt.Printf("变更计划 (dry-run, 未签发任何凭证):\n  目标:   %s\n  配置:   %s\n  协议:   %s\n  模型:   %s\n  base_url: %s\n\n将写入以下键:\n",
				adapter.DisplayName(), configPath, adapter.Protocol(), modelID, baseURL)
			for _, k := range sortedKeys(plan) {
				fmt.Printf("  %s = %v\n", k, plan[k])
			}
			fmt.Println("\n安全影响: 配置文件将包含一枚可代表你的会员权益调用模型的长期凭证; 文件权限会被设为 0600。")
			return nil
		}

		// ── 1) 签发凭证 ──
		fmt.Printf("正在为 %s 签发会员智能体凭证…\n", adapter.DisplayName())
		created, err := client.CreateAgentAccessKey(ctx, acosmi.CreateAgentAccessKeyRequest{
			Name:        "acosmi-cli/" + adapter.ID(),
			TargetAgent: adapter.ID(),
			Purpose:     "cli-agent-add",
			ExpiresDays: flagAgentExpiry,
			// 模型 allowlist 收窄到本次实际要用的那一个 —— 最小权限。
			// 用户想放开可在控制台改。
			Models: []string{modelID},
		})
		if err != nil {
			return fmt.Errorf("签发凭证失败: %w", err)
		}
		fmt.Printf("已签发: %s (有效期至 %s)\n", created.Key.KeyPrefix, formatExpiry(created.Key.ExpiresAt))

		// 从这里开始, 任何失败都必须把凭证吊销掉 —— 否则会留下一枚没人用却能花钱的凭证。
		rollbackKey := func(reason error) error {
			if rerr := client.RevokeAgentAccessKey(context.Background(), created.Key.ID); rerr != nil {
				return fmt.Errorf("%w (且自动吊销凭证失败: %v —— 请到控制台手动吊销 %s)", reason, rerr, created.Key.KeyPrefix)
			}
			return fmt.Errorf("%w (已自动吊销刚签发的凭证)", reason)
		}

		// ── 2) 写配置 ──
		plan, err := adapter.Plan(AgentSetup{
			BaseURL: baseURL, APIKey: created.Plaintext, ModelID: modelID, ServerURL: cfg.ServerURL,
		})
		if err != nil {
			return rollbackKey(err)
		}
		preExisting, backup, err := applyPatch(configPath, plan)
		if err != nil {
			return rollbackKey(fmt.Errorf("写入配置失败: %w", err))
		}
		fmt.Printf("已写入配置: %s\n", configPath)
		if backup != "" {
			fmt.Printf("原配置已备份: %s\n", backup)
		}

		// ── 3) 记 manifest (绝不记明文) ──
		entry := &AgentEntry{
			Target: adapter.ID(), CredentialID: created.Key.ID, KeyPrefix: created.Key.KeyPrefix,
			ConfigPath: configPath, BackupPath: backup,
			PatchKeys: sortedKeys(plan), PreExisting: preExisting,
			BaseURL: baseURL, ModelID: modelID, Protocol: adapter.Protocol(),
			ServerURL: cfg.ServerURL, CreatedAt: time.Now().UTC(),
		}
		m := loadManifest()
		m.Agents[adapter.ID()] = entry
		if err := saveManifest(m); err != nil {
			_ = rollbackPatch(entry)
			return rollbackKey(fmt.Errorf("写 manifest 失败: %w", err))
		}

		// ── 4) 验证 ──
		if err := verifyAgent(ctx, entry, created.Plaintext, !flagAgentNoVerify); err != nil {
			fmt.Fprintf(os.Stderr, "验证未通过: %v\n", err)
			fmt.Fprintln(os.Stderr, "正在回滚…")
			_ = rollbackPatch(entry)
			delete(m.Agents, adapter.ID())
			_ = saveManifest(m)
			return rollbackKey(err)
		}

		fmt.Printf("\n完成。%s 现在可以使用 Acosmi 模型 %s 了。\n", adapter.DisplayName(), modelID)
		fmt.Println("说明: 该凭证消费的是你本人的会员订阅权益; 多枚凭证共享同一份总额度。")
		fmt.Printf("卸载: acosmi agent remove %s\n", adapter.ID())
		return nil
	},
}

// resolveModel --model auto 时取账号可用的第一个模型。
func resolveModel(ctx context.Context, requested string) (string, error) {
	if requested != "" && requested != "auto" {
		return requested, nil
	}
	models, err := client.ListModels(ctx)
	if err != nil {
		return "", fmt.Errorf("拉取可用模型失败 (可显式指定 --model): %w", err)
	}
	if len(models) == 0 {
		return "", fmt.Errorf("当前账号没有可用模型; 请先开通订阅或用 --model 指定")
	}
	return models[0].ModelID, nil
}

func formatExpiry(t *time.Time) string {
	if t == nil {
		return "未设置"
	}
	return t.Local().Format("2006-01-02 15:04")
}

// =============================================================================
// list / doctor / rotate / remove
// =============================================================================

var agentListCmd = &cobra.Command{
	Use:   "list",
	Short: "列出本机已接入的智能体",
	RunE: func(cmd *cobra.Command, args []string) error {
		m := loadManifest()
		if len(m.Agents) == 0 {
			fmt.Println("本机尚未接入任何智能体。试试: acosmi agent add crabcode --login")
			return nil
		}
		if flagJSON {
			raw, _ := json.MarshalIndent(m.Agents, "", "  ")
			fmt.Println(string(raw))
			return nil
		}
		ids := make([]string, 0, len(m.Agents))
		for id := range m.Agents {
			ids = append(ids, id)
		}
		sort.Strings(ids)
		fmt.Printf("%-20s %-16s %-28s %s\n", "目标", "凭证", "模型", "配置文件")
		for _, id := range ids {
			e := m.Agents[id]
			fmt.Printf("%-20s %-16s %-28s %s\n", e.Target, e.KeyPrefix, e.ModelID, e.ConfigPath)
		}
		return nil
	},
}

var agentDoctorCmd = &cobra.Command{
	Use:   "doctor <目标>",
	Short: "体检: 配置是否还在、凭证是否还有效、最近用量",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		m := loadManifest()
		entry, ok := m.Agents[args[0]]
		if !ok {
			return fmt.Errorf("%q 未由本 CLI 接入过 (acosmi agent list 可查看已接入目标)", args[0])
		}
		ctx := context.Background()

		fmt.Printf("目标:     %s\n配置文件: %s\n模型:     %s\nbase_url: %s\n凭证:     %s\n\n",
			entry.Target, entry.ConfigPath, entry.ModelID, entry.BaseURL, entry.KeyPrefix)

		// ① 配置文件还在吗、我们的键还在吗
		obj, err := readJSONObject(entry.ConfigPath)
		if err != nil {
			fmt.Printf("[×] 配置文件读取失败: %v\n", err)
		} else {
			missing := []string{}
			for _, k := range entry.PatchKeys {
				if !dottedExists(obj, k) {
					missing = append(missing, k)
				}
			}
			if len(missing) == 0 {
				fmt.Println("[✓] 配置项完整")
			} else {
				fmt.Printf("[×] 配置项缺失: %s (可重新运行 acosmi agent add %s 修复)\n",
					strings.Join(missing, ", "), entry.Target)
			}
		}

		// ② 凭证在服务端还有效吗
		if err := requireAuth(); err != nil {
			fmt.Println("[!] 未登录, 跳过服务端检查 (acosmi auth login 后重试)")
			return nil
		}
		keys, err := client.ListAgentAccessKeys(ctx)
		if err != nil {
			fmt.Printf("[!] 查询服务端凭证失败: %v\n", err)
			return nil
		}
		found := false
		for _, k := range keys {
			if k.ID == entry.CredentialID {
				found = true
				fmt.Printf("[✓] 凭证有效, 到期 %s\n", formatExpiry(k.ExpiresAt))
			}
		}
		if !found {
			fmt.Println("[×] 服务端已无该凭证 (可能已吊销或过期) —— 运行 acosmi agent rotate " + entry.Target + " 重签")
			return nil
		}

		// ③ 最近用量
		rows, err := client.AgentAccessKeyUsage(ctx, entry.CredentialID, flagAgentUsageDays)
		if err != nil {
			fmt.Printf("[!] 用量查询失败: %v\n", err)
			return nil
		}
		if len(rows) == 0 {
			fmt.Printf("[i] 最近 %d 天无调用记录\n", flagAgentUsageDays)
			return nil
		}
		fmt.Printf("\n最近 %d 天用量:\n%-28s %8s %12s %12s\n", flagAgentUsageDays, "模型", "调用", "输入 token", "输出 token")
		for _, r := range rows {
			fmt.Printf("%-28s %8d %12d %12d\n", r.ModelID, r.Calls, r.InputTokens, r.OutputTokens)
		}
		return nil
	},
}

var agentRotateCmd = &cobra.Command{
	Use:   "rotate <目标>",
	Short: "轮换凭证 (签发新的 + 吊销旧的 + 更新配置)",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := requireAuth(); err != nil {
			return err
		}
		m := loadManifest()
		entry, ok := m.Agents[args[0]]
		if !ok {
			return fmt.Errorf("%q 未由本 CLI 接入过", args[0])
		}
		adapter, err := lookupAdapter(entry.Target)
		if err != nil {
			return err
		}
		ctx := context.Background()

		created, err := client.RotateAgentAccessKey(ctx, entry.CredentialID)
		if err != nil {
			return fmt.Errorf("轮换失败: %w", err)
		}
		plan, err := adapter.Plan(AgentSetup{
			BaseURL: entry.BaseURL, APIKey: created.Plaintext, ModelID: entry.ModelID, ServerURL: entry.ServerURL,
		})
		if err != nil {
			return err
		}
		preExisting, backup, err := applyPatch(entry.ConfigPath, plan)
		if err != nil {
			// 新凭证已签发但配置没写进去: 吊销新凭证, 旧的已被服务端吊销 —— 明确告诉用户重跑 add。
			_ = client.RevokeAgentAccessKey(context.Background(), created.Key.ID)
			return fmt.Errorf("写入新凭证失败, 已吊销新凭证; 旧凭证也已失效, 请重新运行 acosmi agent add %s: %w",
				entry.Target, err)
		}
		entry.CredentialID = created.Key.ID
		entry.KeyPrefix = created.Key.KeyPrefix
		entry.PatchKeys = sortedKeys(plan)
		entry.PreExisting = preExisting
		if backup != "" {
			entry.BackupPath = backup
		}
		if err := saveManifest(m); err != nil {
			return fmt.Errorf("轮换已完成但 manifest 写入失败 (下次 remove 可能不完整): %w", err)
		}
		fmt.Printf("已轮换: 新凭证 %s, 旧凭证已失效。\n", created.Key.KeyPrefix)
		return nil
	},
}

var agentRemoveCmd = &cobra.Command{
	Use:   "remove <目标>",
	Short: "卸载: 撤销本 CLI 写入的配置 + 吊销本 CLI 签发的凭证",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		m := loadManifest()
		entry, ok := m.Agents[args[0]]
		if !ok {
			return fmt.Errorf("%q 未由本 CLI 接入过", args[0])
		}

		// 先撤配置再吊凭证: 反过来的话, 万一配置撤销失败, 目标智能体会带着一枚已失效的凭证
		// 反复报错, 而用户手里没有任何线索。
		if err := rollbackPatch(entry); err != nil {
			return fmt.Errorf("撤销配置失败 (未吊销凭证, 可重试): %w", err)
		}
		fmt.Printf("已从 %s 撤销本 CLI 写入的配置项\n", entry.ConfigPath)

		if client != nil && client.IsAuthorized() {
			if err := client.RevokeAgentAccessKey(context.Background(), entry.CredentialID); err != nil {
				fmt.Fprintf(os.Stderr, "[!] 凭证吊销失败: %v\n请到开放平台控制台手动吊销 %s\n", err, entry.KeyPrefix)
			} else {
				fmt.Printf("已吊销凭证 %s\n", entry.KeyPrefix)
			}
		} else {
			fmt.Fprintf(os.Stderr, "[!] 未登录, 未能吊销凭证 %s —— 请登录后运行 acosmi agent remove %s, 或到控制台吊销\n",
				entry.KeyPrefix, entry.Target)
		}

		delete(m.Agents, entry.Target)
		if err := saveManifest(m); err != nil {
			return fmt.Errorf("更新 manifest 失败: %w", err)
		}
		return nil
	},
}

// dottedExists 判断点分路径在配置里是否还存在 (doctor 用)。
func dottedExists(obj map[string]json.RawMessage, dotted string) bool {
	parts := strings.Split(dotted, ".")
	cur := obj
	for i, p := range parts {
		v, ok := cur[p]
		if !ok {
			return false
		}
		if i == len(parts)-1 {
			return true
		}
		child := map[string]json.RawMessage{}
		if err := json.Unmarshal(v, &child); err != nil {
			return false
		}
		cur = child
	}
	return true
}
