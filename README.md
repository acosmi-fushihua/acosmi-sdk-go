# Acosmi Go SDK

Acosmi 平台官方 Go SDK，提供统一 API 客户端和 CrabClaw-Skill 命令行工具。

一次 OAuth 授权即可访问全域 API：AI 模型对话、权益管理、流量包商城、钱包、技能商店、工具列表、WebSocket 实时推送。

## 快速开始

### 作为 Go 库使用

```bash
go get github.com/acosmi/acosmi-sdk-go
```

```go
package main

import (
    "context"
    "fmt"
    "log"

    acosmi "github.com/acosmi/acosmi-sdk-go"
)

func main() {
    client, _ := acosmi.NewClient(acosmi.Config{
        ServerURL: "https://acosmi.com", // 默认大陆站 (零值也是此值); 国际站传 https://acosmi.ai; 本地开发 http://127.0.0.1:3300
    })

    ctx := context.Background()

    // 首次登录（打开浏览器）
    if !client.IsAuthorized() {
        client.Login(ctx, "MyApp", acosmi.AllScopes())
    }

    // 查询余额
    balance, _ := client.GetBalance(ctx)
    fmt.Printf("Token 剩余: %d\n", balance.TotalTokenRemaining)

    // 流式聊天
    models, _ := client.ListModels(ctx)
    events, errs := client.ChatStream(ctx, models[0].ID, acosmi.ChatRequest{
        Messages: []acosmi.ChatMessage{{Role: "user", Content: "你好"}},
    })
    for e := range events {
        fmt.Print(e.Data)
    }
    if err := <-errs; err != nil {
        log.Fatal(err)
    }
}
```

### 作为 CLI 工具使用

```bash
# 从源码构建
git clone https://github.com/acosmi/acosmi-sdk-go.git
cd acosmi-sdk-go
make build
./bin/crabclaw-skill login

# 或通过 NPM 安装
npm install -g @acosmi/crabclaw-skill
crabclaw-skill login
```

详细文档请阅读 [开发手册 (docs/guide.md)](docs/guide.md)。

## 许可证

MIT License
