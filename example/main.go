package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"time"

	acosmi "github.com/acosmi/acosmi-sdk-go"
)

func main() {
	// nexus-v4 服务地址 (SDK 自动追加 /api/v4, 无需手动拼接)
	// 生产环境默认 https://acosmi.com (大陆); 国际站 https://acosmi.ai
	// 本地开发: export ACOSMI_SERVER_URL=http://127.0.0.1:3300
	serverURL := os.Getenv("ACOSMI_SERVER_URL")
	if serverURL == "" {
		serverURL = "https://acosmi.com"
	}

	// 1. 创建客户端 (合并后统一入口, 一次授权覆盖全域 API)
	client, err := acosmi.NewClient(acosmi.Config{
		ServerURL: serverURL,
		// Store: 默认文件存储 ~/.acosmi/tokens.json
		// 生产环境可替换为系统钥匙串:
		// Store: &KeychainTokenStore{service: "com.acosmi.desktop"},
	})
	if err != nil {
		log.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	// 2. 首次使用需要登录 (会打开浏览器, 一次授权获取全部 scope)
	if !client.IsAuthorized() {
		fmt.Println("首次使用，将打开浏览器进行授权...")
		err := client.Login(ctx, "CrabClaw-Skill Desktop Agent", acosmi.AllScopes())
		if err != nil {
			log.Fatalf("授权失败: %v", err)
		}
		fmt.Println("授权成功!")
	}

	// 3. 查询权益余额
	balance, err := client.GetBalance(ctx)
	if err != nil {
		log.Fatalf("查询余额失败: %v", err)
	}
	fmt.Printf("Token 余额: %d / %d (剩余 %d)\n",
		balance.TotalTokenUsed, balance.TotalTokenQuota, balance.TotalTokenRemaining)
	fmt.Printf("调用次数: %d / %d (剩余 %d)\n",
		balance.TotalCallUsed, balance.TotalCallQuota, balance.TotalCallRemaining)

	// 4. 查询钱包统计
	wallet, err := client.GetWalletStats(ctx)
	if err != nil {
		fmt.Printf("查询钱包失败 (非致命): %v\n", err)
	} else {
		fmt.Printf("钱包余额: %s | 本月消费: %s | 本月充值: %s\n",
			wallet.Balance, wallet.MonthlyConsumption, wallet.MonthlyRecharge)
	}

	// 5. 浏览流量包商城
	packages, err := client.ListTokenPackages(ctx)
	if err != nil {
		fmt.Printf("查询商城失败 (非致命): %v\n", err)
	} else {
		fmt.Printf("\n流量包商城 (%d 个):\n", len(packages))
		for _, p := range packages {
			fmt.Printf("  - %s: 原价 %.2f 元 / 活动价 %.2f 元 / %s\n",
				p.Name,
				float64(p.OriginalPriceCent)/100,
				float64(p.CampaignPriceCent)/100,
				p.BillingCycle)
		}
	}

	// 6. 浏览技能商店
	skills, err := client.BrowseSkillStore(ctx, acosmi.SkillStoreQuery{})
	if err != nil {
		fmt.Printf("浏览技能商店失败 (非致命): %v\n", err)
	} else {
		fmt.Printf("\n技能商店 (%d 个公共技能):\n", len(skills))
		for _, s := range skills {
			fmt.Printf("  - %s [%s] (%s) 下载: %d\n", s.Name, s.Category, s.Author, s.DownloadCount)
		}
	}

	// 7. 获取已安装的工具
	tools, err := client.ListTools(ctx)
	if err != nil {
		fmt.Printf("获取工具列表失败 (非致命): %v\n", err)
	} else {
		fmt.Printf("\n已安装工具 (%d 个):\n", len(tools))
		for _, t := range tools {
			provider := "unknown"
			if t.Provider != nil {
				provider = t.Provider.Name
			}
			fmt.Printf("  - %s [%s] (来源: %s)\n", t.Name, t.Category, provider)
		}
	}

	// 8. 获取可用模型
	models, err := client.ListModels(ctx)
	if err != nil {
		log.Fatalf("获取模型列表失败: %v", err)
	}
	fmt.Printf("\n可用模型 (%d 个):\n", len(models))
	for _, m := range models {
		fmt.Printf("  - %s (%s / %s)\n", m.Name, m.Provider, m.ModelID)
	}

	if len(models) == 0 {
		fmt.Println("没有可用模型，请管理员先配置托管模型")
		return
	}

	// 9. 建立 WebSocket 长连接 (实时推送)
	fmt.Println("\n建立 WebSocket 长连接...")
	err = client.Connect(ctx, acosmi.WSConfig{
		Topics: []string{"balance", "skills", "system"},
		OnEvent: func(e acosmi.WSEvent) {
			fmt.Printf("[WS 事件] type=%s topic=%s data=%s\n", e.Type, e.Topic, string(e.Data))
		},
		OnConnect: func() {
			fmt.Println("[WS] 已连接")
		},
		OnDisconnect: func(err error) {
			fmt.Printf("[WS] 断开: %v\n", err)
		},
	})
	if err != nil {
		fmt.Printf("WebSocket 连接失败 (非致命): %v\n", err)
	} else {
		defer client.Disconnect()
		fmt.Printf("WebSocket 已连接: %v\n", client.IsConnected())
	}

	// 10. 流式聊天 (使用 ChatStreamWithUsage 自动获取实时余额)
	modelID := models[0].ID
	fmt.Printf("\n使用模型 %s 进行对话:\n", models[0].Name)

	contentCh, sourcesCh, settleCh, errCh := client.ChatStreamWithUsage(ctx, modelID, acosmi.ChatRequest{
		Messages: []acosmi.ChatMessage{
			{Role: "user", Content: "用一句话介绍你自己"},
		},
		MaxTokens: 256,
	})

	// 并发消费 sources channel (搜索来源)
	go func() {
		for src := range sourcesCh {
			fmt.Printf("\n[搜索来源: %d 条]\n", len(src.Sources))
			for _, s := range src.Sources {
				fmt.Printf("  - %s %s\n", s.Title, s.URL)
			}
		}
	}()

	fmt.Print("AI: ")
	for event := range contentCh {
		var chunk struct {
			Choices []struct {
				Delta struct {
					Content string `json:"content"`
				} `json:"delta"`
			} `json:"choices"`
		}
		if err := parseJSON(event.Data, &chunk); err == nil && len(chunk.Choices) > 0 {
			fmt.Print(chunk.Choices[0].Delta.Content)
		}
	}
	fmt.Println()

	// 读取结算信息 (token 消耗 + 剩余余额)
	if settle, ok := <-settleCh; ok {
		fmt.Printf("\n消耗: %d token (输入 %d + 输出 %d)\n",
			settle.TotalTokens, settle.InputTokens, settle.OutputTokens)
		if settle.TokenRemaining >= 0 {
			fmt.Printf("剩余: %d token / %d 次调用\n",
				settle.TokenRemaining, settle.CallRemaining)
		}
	}

	if err := <-errCh; err != nil {
		log.Fatalf("流式聊天错误: %v", err)
	}
}

func parseJSON(data string, v interface{}) error {
	return json.Unmarshal([]byte(data), v)
}
