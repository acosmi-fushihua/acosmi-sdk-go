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

    // 流式聊天 (v1.6.0+ 推荐传 EndUserID 启用上游用户隔离 / KV-cache / 调度三项策略)
    models, _ := client.ListModels(ctx)
    events, errs := client.ChatStream(ctx, models[0].ID, acosmi.ChatRequest{
        Messages:  []acosmi.ChatMessage{{Role: "user", Content: "你好"}},
        EndUserID: "user-abc-123", // 业务侧稳定 id, 非 PII; 不传时网关从认证身份自动派生
    })
    for e := range events {
        fmt.Print(e.Data)
    }
    if err := <-errs; err != nil {
        log.Fatal(err)
    }
}
```

> **v1.6.0 新特性**: `EndUserID` 字段 + 自动 SSE 保活解析 + 11min per-request 超时 (覆盖
> DeepSeek 等上游 "开始推理前最长 10min 保活" 窗口)。详见 [开发手册 §13 §14](docs/guide.md)。

### 图片 / 视频生成 (v1.1.0+)

图片/视频生成与文本对话**同属托管模型网关**(同一个 `Client`、同一套 `models:chat` 鉴权面),**不是工作流**。只有 `Capabilities.SupportsImageGeneration` / `SupportsVideoGeneration` 为真的模型可调;计费结算在营销系统,SDK / 网关只做调用与用量上报。

```go
// 先按 capability 筛模型
models, _ := client.ListModels(ctx)
var imageModelID, videoModelID string
for _, m := range models {
    if m.Capabilities.SupportsImageGeneration { imageModelID = m.ID }
    if m.Capabilities.SupportsVideoGeneration { videoModelID = m.ID }
}

// 图片 (同步): 一次调用直接拿图。无 deadline 时默认 11min 超时容纳上游。
img, _ := client.GenerateImage(ctx, imageModelID, &acosmi.ImageGenerationRequest{
    Prompt: "一只在雪地里奔跑的柴犬,电影感光影",
    Width:  1024, // 缺省 1024
    Height: 1024, // 缺省 1024
    Style:  "cinematic",
})
fmt.Println(img.URL) // 或 img.B64JSON / img.RevisedPrompt

// 视频 (异步): 建任务 → 轮询。durationSeconds 回传创建时秒数, 网关在 completed 时据此上报时长用量。
task, _ := client.GenerateVideo(ctx, videoModelID, &acosmi.VideoGenerationRequest{
    Prompt:     "海浪拍打礁石的慢镜头",
    Resolution: "1280x720",
    Duration:   5, // 秒
})
res := task
for res.Status != "completed" && res.Status != "failed" {
    time.Sleep(3 * time.Second)
    res, _ = client.PollVideoTask(ctx, videoModelID, task.TaskID, 5) // 回传 duration=5
}
fmt.Println(res.VideoURL) // 失败时 res.Error
```

> 字段是网关**通用契约**(图片 `Prompt`/`Width`/`Height`/`Style`; 视频 `Prompt`/`Resolution`/`Duration`);
> 某厂商支持哪些取值由上游模型决定。网关适配 OpenAI 兼容图片 + 火山引擎(即梦/豆包)视频 +
> DashScope 通义万相(wanx)原生异步任务(图片+视频)。详见 [开发手册 §v1.1.0](docs/guide.md)。

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
