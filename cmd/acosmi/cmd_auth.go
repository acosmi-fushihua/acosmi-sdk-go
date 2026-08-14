package main

// =============================================================================
// acosmi auth —— 登录 / 登出 / 身份
//
// 两条登录通道:
//   --browser (默认)  本机 loopback 授权码流 + PKCE。要求能开监听端口且能拉起浏览器。
//   --device          RFC 8628 设备流。SSH / 容器 / CI 唯一可行的方式。
//
// scope 默认申请 `ai` + `agent_access:manage`: 前者是模型调用, 后者是签发智能体凭证的权限。
// agent_access:manage **不在** SDK 的 AllScopes 里, 必须显式申请 —— 用户会在同意页看到它,
// 那是这项高风险能力唯一的人类闸门 (方案红线 12)。
// =============================================================================

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	acosmi "github.com/acosmi/acosmi-sdk-go"
	"github.com/spf13/cobra"
)

var (
	flagLoginDevice  bool
	flagLoginBrowser bool
	flagLoginScopes  string
	flagLoginForce   bool
)

func init() {
	authCmd.AddCommand(authLoginCmd, authLogoutCmd, authWhoamiCmd)
	authLoginCmd.Flags().BoolVar(&flagLoginDevice, "device", false, "设备授权流 (RFC 8628) — SSH / 容器 / 无浏览器环境")
	authLoginCmd.Flags().BoolVar(&flagLoginBrowser, "browser", false, "本机浏览器 + loopback 授权码流 (默认)")
	authLoginCmd.Flags().StringVar(&flagLoginScopes, "scope", "", "逗号分隔的 scope (默认 ai,agent_access:manage)")
	authLoginCmd.Flags().BoolVarP(&flagLoginForce, "force", "f", false, "已登录时强制重新授权")
	rootCmd.AddCommand(authCmd)
}

var authCmd = &cobra.Command{Use: "auth", Short: "登录与身份管理"}

// defaultLoginScopes 默认申请集。
// ai = 模型调用/权益查询; agent_access:manage = 签发会员委托凭证 (agent add 需要它)。
func defaultLoginScopes() []string {
	return append(acosmi.ModelScopes(), acosmi.ScopeAgentAccessManage)
}

func parseScopes(raw string) []string {
	if strings.TrimSpace(raw) == "" {
		return defaultLoginScopes()
	}
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if s := strings.TrimSpace(p); s != "" {
			out = append(out, s)
		}
	}
	if len(out) == 0 {
		return defaultLoginScopes()
	}
	return out
}

var authLoginCmd = &cobra.Command{
	Use:   "login",
	Short: "OAuth 登录 (浏览器或设备流)",
	RunE: func(cmd *cobra.Command, args []string) error {
		if client.IsAuthorized() && !flagLoginForce {
			fmt.Println("已登录。需要重新授权请加 --force")
			return nil
		}
		scopes := parseScopes(flagLoginScopes)
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
		defer cancel()

		if flagLoginDevice {
			return loginWithDevice(ctx, scopes)
		}
		fmt.Println("正在打开浏览器完成授权…")
		fmt.Printf("申请权限: %s\n", strings.Join(scopes, " "))
		if err := client.Login(ctx, "Acosmi CLI", scopes); err != nil {
			return fmt.Errorf("授权失败: %w", err)
		}
		fmt.Println("授权成功。凭证已保存到 ~/.acosmi/tokens.json")
		return nil
	},
}

func loginWithDevice(ctx context.Context, scopes []string) error {
	err := client.LoginWithDeviceFlow(ctx, "Acosmi CLI", scopes, func(auth *acosmi.DeviceAuthorization) {
		fmt.Println()
		fmt.Println("请在**任意**有浏览器的设备上打开:")
		fmt.Printf("    %s\n", auth.VerificationURI)
		fmt.Printf("并输入配对码: %s\n", auth.UserCode)
		if auth.VerificationURIComplete != "" {
			fmt.Printf("(免输码直达链接: %s)\n", auth.VerificationURIComplete)
		}
		fmt.Println()
		fmt.Println("等待确认中… (完成后本命令会自动继续)")
	})
	if errors.Is(err, acosmi.ErrDeviceFlowUnsupported) {
		return fmt.Errorf("该服务端尚未开放设备授权流; 请改用 acosmi auth login --browser")
	}
	if err != nil {
		return fmt.Errorf("设备授权失败: %w", err)
	}
	fmt.Println("授权成功。凭证已保存到 ~/.acosmi/tokens.json")
	return nil
}

var authLogoutCmd = &cobra.Command{
	Use:   "logout",
	Short: "清除本机保存的登录凭证",
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := client.Logout(context.Background()); err != nil {
			return fmt.Errorf("登出失败: %w", err)
		}
		fmt.Println("已清除本机登录凭证。")
		fmt.Println("提示: 这不会吊销已签发的智能体凭证 —— 用 acosmi agent remove <目标> 或在控制台吊销。")
		return nil
	},
}

var authWhoamiCmd = &cobra.Command{
	Use:   "whoami",
	Short: "显示当前登录状态",
	RunE: func(cmd *cobra.Command, args []string) error {
		if !client.IsAuthorized() {
			fmt.Println("未登录。运行: acosmi auth login")
			return nil
		}
		fmt.Println("已登录。")
		return nil
	},
}
