# Acosmi Go SDK 开发手册

> v0.11.0 | Go 1.22+ | MIT

## 目录

- [1. 概述](#1-概述)
- [2. 安装](#2-安装)
- [3. 认证机制](#3-认证机制)
- [4. SDK 客户端 API](#4-sdk-客户端-api)
  - [4.1 创建客户端](#41-创建客户端)
  - [4.2 授权](#42-授权)
  - [4.3 AI 模型服务](#43-ai-模型服务)
  - [4.4 权益管理](#44-权益管理)
  - [4.5 流量包商城](#45-流量包商城)
  - [4.6 钱包](#46-钱包)
  - [4.7 技能商店](#47-技能商店)
  - [4.8 WebSocket 实时推送](#48-websocket-实时推送)
  - [4.9 通知管理](#49-通知管理)
  - [4.10 设备注册 (推送通知)](#410-设备注册-推送通知)
  - [4.11 通知偏好](#411-通知偏好)
- [5. CLI 命令手册](#5-cli-命令手册)
- [6. 数据类型参考](#6-数据类型参考)
- [7. 完整示例](#7-完整示例)
- [8. 安全特性](#8-安全特性)
- [9. 项目结构](#9-项目结构)
- [10. 构建与发布](#10-构建与发布)
- [11. 常见问题](#11-常见问题)
- [12. 版本记录](#12-版本记录)

---

## 1. 概述

Acosmi Go SDK 提供两种使用方式:

1. **Go 库** — `import` 引入，类型安全地访问 Acosmi 全域 API
2. **CrabClaw-Skill CLI** — 技能搜索、安装、上传、AI 生成的命令行工具

| 特性 | 说明 |
|------|------|
| OAuth 2.1 PKCE | 安全桌面授权，自动 token 刷新 |
| 统一客户端 | 一个 `Client` 覆盖全域 API |
| 流式聊天 | SSE 实时对话 + 结算余额推送 |
| 多厂商 Adapter | per-provider 路由: Anthropic 格式 / OpenAI 兼容格式自动切换 |
| Beta 自动组装 | 根据模型能力自动注入 10 项 beta header (仅 Anthropic 格式) |
| Server Tool | 联网搜索等服务端工具自动合入请求体 (仅 Anthropic 格式) |
| 模型能力矩阵 | 18 项能力标记，驱动 UI 功能开关 |
| 思考级别 API | v0.9.0 三档 `off`/`high`/`max`，自动组装 thinking+effort+maxTokens |
| WebSocket | 实时余额/技能/系统推送，自动断线重连 |
| 线程安全 | `sync.RWMutex` 保护所有共享状态 |

### 服务地址

| 环境 | 地址 |
|------|------|
| **默认 (大陆站)** | `https://acosmi.com` (零配置) |
| 国际站 | `https://acosmi.ai` (显式传入 `ServerURL`) |
| 本地开发 | `http://127.0.0.1:3300` |

SDK 自动追加 `/api/v4`，无需手动拼接。`acosmi.com` 与 `acosmi.ai` 端点完全兼容，按业务区域选择即可。

**切换环境** (优先级从高到低):
```bash
export ACOSMI_SERVER_URL=http://127.0.0.1:3300       # 环境变量
crabclaw-skill --server http://127.0.0.1:3300 <命令>  # CLI 参数
crabclaw-skill config set server http://127.0.0.1:3300 # 配置文件
```

### 依赖

`github.com/fatih/color` + `gorilla/websocket` + `spf13/cobra`，无 CGO。

---

## 2. 安装

### Go 库

```bash
go get github.com/acosmi/acosmi-sdk-go
```

```go
import acosmi "github.com/acosmi/acosmi-sdk-go"
```

要求 Go 1.22+。

### 构建 CLI

```bash
git clone https://github.com/acosmi/acosmi-sdk-go.git && cd acosmi-sdk-go
make build       # → bin/crabclaw-skill
make build-all   # → dist/crabclaw-skill-{os}-{arch}
make install     # → $GOPATH/bin
```

### NPM 安装 CLI

```bash
npm install -g @acosmi/crabclaw-skill
```

自动下载对应平台预编译二进制。可通过 `CRABCLAW_SKILL_BINARY_PATH` 指定自定义路径。

---

## 3. 认证机制

### OAuth 2.1 PKCE 流程

SDK 使用 Authorization Code + PKCE (S256) 进行桌面端认证:

1. 发现 OAuth 服务端元数据 (`/.well-known/oauth-authorization-server`)
2. 动态注册客户端 (`POST /register`)
3. 启动本地 `127.0.0.1` 随机端口回调服务器 (符合 RFC 8252)
4. 打开浏览器授权 (`/authorize?code_challenge=...`)
5. 接收回调，交换 token (`POST /token`)
6. 保存 token，自动刷新

Token 有效期: Access 15min / Refresh 7d，过期前 30s 自动刷新。

### Scope 权限分组

| Scope | 常量 | 权限范围 |
|-------|------|----------|
| `ai` | `ScopeAI` | 模型调用 + 流量包 + 权益 |
| `skills` | `ScopeSkills` | 技能商店 + 工具 + 执行 |
| `account` | `ScopeAccount` | 个人资料 + 钱包 + 交易 |

```go
acosmi.AllScopes()      // ["ai", "skills", "account"] — 推荐
acosmi.ModelScopes()    // ["ai"]
acosmi.CommerceScopes() // ["ai", "account"]
acosmi.SkillScopes()    // ["skills"]
```

### Token 存储

默认保存到 `~/.acosmi/tokens.json` (目录 `0700`，文件 `0600`)。

实现 `TokenStore` 接口可替换:

```go
type TokenStore interface {
    Save(tokens *TokenSet) error
    Load() (*TokenSet, error)
    Clear() error
}
```

---

## 4. SDK 客户端 API

### 4.1 创建客户端

```go
client, err := acosmi.NewClient(acosmi.Config{
    ServerURL:  "",                   // 零值 → https://acosmi.com (默认); 国际站传 https://acosmi.ai
    Store:      nil,                  // 默认 ~/.acosmi/tokens.json
    HTTPClient: nil,                  // 默认无全局超时 (避免截断 SSE 流)
})
```

> `HTTPClient` 不设全局 `Timeout` 是有意为之 — 全局超时会截断流式聊天。通过 `context.Context` 控制超时。

### 4.2 授权

```go
// 检查 → 登录 → 登出
if !client.IsAuthorized() {
    client.Login(ctx, "我的应用", acosmi.AllScopes()) // 自动开浏览器
}
client.Logout(ctx) // 吊销 + 清除本地

// Token 信息
ts := client.GetTokenSet()
fmt.Printf("过期: %v, 范围: %s\n", ts.ExpiresAt, ts.Scope)
```

#### LoginWithHandler (CrabCode 适配)

带事件回调的登录流程，可监听授权 URL、完成、错误等事件:

```go
client.LoginWithHandler(ctx, "CrabCode Desktop", acosmi.AllScopes(),
    func(e acosmi.LoginEvent) {
        switch e.Type {
        case acosmi.EventAuthURL:
            fmt.Printf("请打开: %s\n", e.URL)
        case acosmi.EventComplete:
            fmt.Println("登录成功")
        case acosmi.EventError:
            fmt.Printf("错误 [%s]: %s\n", e.ErrCode, e.Error)
        }
    },
    acosmi.WithSkipBrowser(), // 可选: 跳过自动打开浏览器
)
```

`LoginErrCode` 分类码: `discovery_failed` / `registration_failed` / `browser_open_failed` / `auth_denied` / `auth_timeout` / `token_exchange_failed` / `ssl_proxy_detected`。

其他 `LoginOption`: `WithLoginHint("user@org.com")` SSO email 预填 / `WithLoginMethod("sso")` / `WithOrgUUID(uuid)` 强制组织登录 / `WithExpiresIn(3600)` 自定义 token 有效期。

### 4.3 AI 模型服务

> scope: `ai`

#### 模型列表

```go
models, _ := client.ListModels(ctx)
for _, m := range models {
    fmt.Printf("%s (%s/%s) 上下文:%d 输出:%d\n",
        m.Name, m.Provider, m.ModelID, m.ContextWindow, m.MaxTokens)
}
```

#### 模型能力查询

```go
caps, _ := client.GetModelCapabilities(ctx, "claude-opus-4-6")
// caps.SupportsThinking / SupportsWebSearch / SupportsFastMode / Supports1MContext ...
```

内部复用 `ListModels` 缓存 (5min TTL)，建议启动时调用一次 `ListModels()` 预热。

#### 同步聊天

> **v0.4.1+ 重要变更**：`Chat()` 的返回结构 `ChatResponse` 已改为 Anthropic 风格的 Content Blocks（`Content []ChatContentBlock`），不论 provider 是 Anthropic 还是 OpenAI 兼容厂商都统一。

```go
resp, _ := client.Chat(ctx, modelID, acosmi.ChatRequest{
    System: "你是一个有帮助的助手",                 // 顶层字段 (Anthropic 约定), 不要塞到 Messages
    Messages: []acosmi.ChatMessage{
        {Role: "user", Content: "Go 语言的优势？"},
    },
    MaxTokens: 1024,
})

// 遍历 content blocks 提取文本 (text / thinking / tool_use 混合出现)
for _, b := range resp.Content {
    if b.Type == "text" {
        fmt.Print(b.Text)
    }
}

// 结算余额 (来自 Header，-1 表示未返回)
if resp.TokenRemaining >= 0 {
    fmt.Printf("剩余: %d token / %d 次调用\n", resp.TokenRemaining, resp.CallRemaining)
}
```

> 若需更便捷的文本提取助手方法 (`TextContent() / ThinkingContent() / ToolUseBlocks()`)，请使用 `ChatMessages()` 返回的 `*AnthropicResponse`（§Anthropic 原生格式小节）。

#### 流式聊天 — ChatStreamWithUsage (推荐)

返回 4 个 channel: 内容 / 搜索来源 / 结算 / 错误。

> **SSE 格式随 provider 而异**：
> - `ChatStream` / `ChatStreamWithUsage` 对 Anthropic provider 透传 Anthropic 原生 SSE（`content_block_delta` 等）；对 OpenAI 兼容 provider 透传 OpenAI SSE（`choices[].delta.content`）。
> - `ChatMessagesStream` 统一输出 Anthropic SSE，对 OpenAI provider 会做格式转换（见 §Anthropic 原生格式）。
>
> 若不想感知差异，推荐用 `ChatMessagesStream` 或在调用前通过 `ListModels()` 缓存中读取 `model.Provider` 分支解析。

```go
contentCh, sourcesCh, settleCh, errCh := client.ChatStreamWithUsage(ctx, modelID, acosmi.ChatRequest{
    Messages:  []acosmi.ChatMessage{{Role: "user", Content: "写一首关于编程的诗"}},
    MaxTokens: 512,
})

go func() {
    for src := range sourcesCh {
        for _, s := range src.Sources {
            fmt.Printf("  [来源] %s %s\n", s.Title, s.URL)
        }
    }
}()

// 解析示例 — 同时兼容 Anthropic 与 OpenAI 两种 SSE payload
for event := range contentCh {
    // 1) Anthropic 风格: {"type":"content_block_delta","delta":{"type":"text_delta","text":"..."}}
    var ant struct {
        Type  string `json:"type"`
        Delta struct {
            Type string `json:"type"`
            Text string `json:"text"`
        } `json:"delta"`
    }
    if json.Unmarshal([]byte(event.Data), &ant) == nil &&
        ant.Type == "content_block_delta" && ant.Delta.Text != "" {
        fmt.Print(ant.Delta.Text)
        continue
    }
    // 2) OpenAI 风格: {"choices":[{"delta":{"content":"..."}}]}
    var oai struct {
        Choices []struct{ Delta struct{ Content string `json:"content"` } `json:"delta"` } `json:"choices"`
    }
    if json.Unmarshal([]byte(event.Data), &oai) == nil && len(oai.Choices) > 0 {
        fmt.Print(oai.Choices[0].Delta.Content)
    }
}

if settle, ok := <-settleCh; ok {
    fmt.Printf("\n消耗: %d token, 剩余: %d\n", settle.TotalTokens, settle.TokenRemaining)
}
if err := <-errCh; err != nil {
    log.Fatal(err)
}
```

#### 流式聊天 — ChatStream (低级 API)

返回原始事件流，需自行处理控制事件 (`started`/`settled`/`pending_settle`/`failed`/`sources`):

```go
eventCh, errCh := client.ChatStream(ctx, modelID, req)
for event := range eventCh {
    if s := acosmi.ParseSettlement(event); s != nil {
        fmt.Printf("消耗: %d token\n", s.TotalTokens)
        continue
    }
    switch event.Event {
    case "started", "pending_settle", "sources":
        continue // 控制事件，跳过
    case "failed":
        log.Printf("stream failed: %s", event.Data)
        continue
    }
    // 解析 chunk...
}
```

#### Anthropic 原生格式 — ChatMessages (V8)

v0.10.0: 路由由模型 `preferred_format` / `supported_formats` 字段驱动, 不再硬编码 provider 名:
- **preferred_format = "anthropic"** → `POST /managed-models/:id/anthropic` (Anthropic 协议)
- **preferred_format = "openai"** → `POST /managed-models/:id/chat` (OpenAI 兼容格式，响应自动转换为 AnthropicResponse)
- **字段为空 (旧 Gateway)** → 回落 v0.5.0 provider 硬编码: Anthropic/Acosmi 走 `/anthropic`, 其他走 `/chat`

调用方无需感知 provider 差异，SDK 内部自动处理格式转换。

**同步调用:**

```go
resp, _ := client.ChatMessages(ctx, modelID, acosmi.ChatRequest{
    RawMessages: []map[string]interface{}{
        {"role": "user", "content": "Go 语言的优势？"},
    },
    MaxTokens: 1024,
})
fmt.Println(resp.TextContent()) // 提取所有 text 块拼接
fmt.Printf("tokens: %d in / %d out\n", resp.Usage.InputTokens, resp.Usage.OutputTokens)
```

**流式调用:**

```go
eventCh, errCh := client.ChatMessagesStream(ctx, modelID, acosmi.ChatRequest{
    RawMessages: []map[string]interface{}{
        {"role": "user", "content": "写一首诗"},
    },
    MaxTokens: 512,
})
for event := range eventCh {
    // Anthropic SSE 事件: message_start, content_block_start, content_block_delta, message_delta, message_stop
    var delta struct {
        Type  string `json:"type"`
        Delta struct {
            Type string `json:"type"`
            Text string `json:"text"`
        } `json:"delta"`
    }
    if json.Unmarshal([]byte(event.Data), &delta) == nil && delta.Delta.Text != "" {
        fmt.Print(delta.Delta.Text)
    }
}
if err := <-errCh; err != nil {
    log.Fatal(err)
}
```

**与 Chat/ChatStream 的区别:**

| | Chat / ChatStream | ChatMessages / ChatMessagesStream |
|---|---|---|
| 端点 | 按 provider 自动路由 | 按 provider 自动路由 |
| 请求类型 | `ChatRequest` (统一) | `ChatRequest` (统一) |
| 响应格式 | `ChatResponse` | `AnthropicResponse` (Content Blocks) |
| 流式控制事件 | started/settled/pending_settle/failed/[DONE] | Anthropic SSE (message_stop 自然结束) |
| Provider 限制 | 所有 provider | 所有 provider (v0.5.0 Adapter 自动转换) |

**v0.10.0 Capability-driven 路由规则:**

SDK 通过 `getAdapterForModel(model)` 按以下**四层优先级**选择 adapter:

1. `model.PreferredFormat == "anthropic"` → AnthropicAdapter
2. `model.PreferredFormat == "openai"` → OpenAIAdapter
3. `model.SupportedFormats` 含 "anthropic" → AnthropicAdapter (否则 "openai" → OpenAIAdapter)
4. 均为空 (旧 Gateway 未返回字段) → 回落 provider 硬编码: `{anthropic, acosmi}` → AnthropicAdapter, 其余 → OpenAIAdapter

**上游默认**（Gateway v0.10.0+ 在 `/models` 响应里填充）:

| Provider | supported_formats | preferred_format | 端点后缀 | Betas 注入 |
|----------|------|------|------|------|
| Anthropic | `["anthropic","openai"]` | `anthropic` | `/anthropic` | 是 (10 项) |
| Acosmi | 同 Anthropic (hardcode 回落) | — | `/anthropic` | 是 |
| DashScope (Qwen) | `["anthropic","openai"]` | `anthropic` | **`/anthropic`** ⚠️ v0.10.0 起改从 `/chat` 切换 | 是 |
| Zhipu (GLM) | `["anthropic","openai"]` | `anthropic` | **`/anthropic`** ⚠️ | 是 |
| DeepSeek | `["anthropic","openai"]` | `anthropic` | **`/anthropic`** ⚠️ | 是 |
| OpenAI | `["openai"]` | `openai` | `/chat` | 否 |
| VolcEngine (豆包) | `["openai"]` | `openai` | `/chat` | 否 |
| Custom | `["openai"]` | `openai` | `/chat` | 否 |

> **⚠️ 破坏性变更 (v0.10.0)**: DashScope / Zhipu / DeepSeek 默认切到 Anthropic 协议端点 — 这三家 Gateway 侧本就内置 Anthropic 兼容端点, 但 v0.9.x 及以前 SDK 按 provider 名硬编码走 `/chat`, 导致 `tool_reference` 等 Anthropic 专属 content block 被 Rust gateway 严格校验 400 拒绝。若需保留旧行为, 手动在 `ManagedModel.PreferredFormat` 置 `"openai"` 或 Gateway 侧只返回 `supported_formats: ["openai"]`。

> 注: OpenAIAdapter 不注入 Anthropic betas，扩展字段 (thinking/effort/speed) 以通用 JSON 透传给 Nexus Gateway，由 Gateway per-provider adapter 转换为厂商格式。

#### 扩展字段 (CrabCode)

所有扩展字段零值不改变行为，基础下游无需修改:

```go
resp, _ := client.Chat(ctx, modelID, acosmi.ChatRequest{
    Messages: []acosmi.ChatMessage{{Role: "user", Content: "分析这段代码"}},

    RawMessages: /* 多模态 content blocks, 非 nil 时优先于 Messages */,
    System:      "你是代码分析助手",
    Tools:       /* 标准工具定义 */,
    Thinking:    &acosmi.ThinkingConfig{Type: "enabled", BudgetTokens: 8192},
    Effort:      &acosmi.EffortConfig{Level: "high"},
    Speed:       "fast",
    OutputConfig: &acosmi.OutputConfig{Format: "json_schema", Schema: /* ... */},
    ServerTools: []acosmi.ServerTool{searchTool},
    Betas:       []string{"my-custom-beta"},
    ExtraBody:   map[string]interface{}{"custom": "value"},
})
```

> 扩展字段标记 `json:"-"`，仅通过内部 `buildChatRequest` 序列化。

#### 联网搜索 (Server Tool)

```go
searchTool, _ := acosmi.NewWebSearchTool(&acosmi.WebSearchConfig{
    MaxUses:        5,
    AllowedDomains: []string{"golang.org"}, // 与 BlockedDomains 互斥
    UserLocation:   &acosmi.GeoLoc{Country: "CN"},
})

eventCh, errCh := client.ChatStream(ctx, modelID, acosmi.ChatRequest{
    Messages:    []acosmi.ChatMessage{{Role: "user", Content: "Go 1.23 新特性？"}},
    ServerTools: []acosmi.ServerTool{searchTool},
})
```

#### Beta Header 自动组装

每次 Chat 调用自动注入适用的 Beta Header，无需手动管理:

| Beta | 条件 |
|------|------|
| `interleaved-thinking-2025-05-14` | 支持 ISP |
| `context-management-2025-06-27` | 支持 ISP |
| `context-1m-2025-08-07` | 支持 1M |
| `structured-outputs-2025-11-13` | 支持 + OutputConfig 非 nil |
| `token-efficient-tools-2025-02-19` | 支持 + OutputConfig 为 nil (与上互斥) |
| `advanced-tool-use-2025-11-20` | 支持 Tool Search |
| `effort-2025-11-24` | 支持 Effort 且 (`Effort 非 nil` 或 `Thinking.Level ∈ {high,max}`) |
| `fast-mode-2026-02-01` | 支持 + Speed == "fast" |
| `prompt-caching-scope-2026-01-05` | 支持 Prompt Cache |
| `redact-thinking-2026-02-12` | 支持 + Thinking.Display == "summary" |

#### 思考级别 (Thinking Level) — v0.9.0

三档语义化入口，SDK 自动组装 `thinking` + `effort` + `max_tokens`，下游无需了解各字段联动细节：

| 常量 | 含义 | thinking 字段 | effort | max_tokens |
|------|------|---------------|--------|-----------|
| `ThinkingOff` (`"off"`) | 关闭思考 | `{type:"disabled"}` | — (不发送) | 不改动 |
| `ThinkingHigh` (`"high"`) | 标准思考 | `{type:"adaptive"}` (Claude 4.x) 或 `{type:"enabled", budget_tokens}` (旧模型) | `high` | 至少 32K (`ThinkingHighMinMaxTokens`) |
| `ThinkingMax` (`"max"`) | 深度思考 | 同上 | `max` (若 `SupportsMaxEffort`) 否则 `high` | 拉到 `caps.MaxOutputTokens`，不可用时回退 128K (`ThinkingMaxFallbackMaxTokens`) |

```go
// 推荐写法 — 用 NewThinkingConfig 构造
req := acosmi.ChatRequest{
    Messages:  []acosmi.ChatMessage{{Role: "user", Content: "证明黎曼猜想"}},
    MaxTokens: 8192,
    Thinking:  acosmi.NewThinkingConfig(acosmi.ThinkingMax), // → adaptive + effort=max + maxTokens=modelMax
}

// 或直接填 Level 字段
req.Thinking = &acosmi.ThinkingConfig{Type: "adaptive", Level: acosmi.ThinkingHigh}
```

**关键行为**:

- **自动覆盖**：Level 非空时，SDK 接管 `thinking` / `effort` / `max_tokens` 的组装，不要再通过 `ExtraBody` 手动覆写这些 key，否则会与 SDK 计算结果冲突。
- **temperature 互斥**：Level 非 `off` 时 SDK 自动删除 `temperature`（Anthropic API 约束）。
- **旧模型回退**：不支持 `adaptive` 的模型回退 `enabled` + `budget_tokens = max_tokens - 1`。
- **betaEffort 自动注入**：Level=`high`/`max` 且模型 `SupportsEffort` 时自动加入 `effort-2025-11-24` beta。
- **v0.8.0 兼容模式**：`Level` 为空字符串时 SDK 保持 passthrough（`Thinking` / `Effort` 结构原样序列化），老代码零影响。

```go
// 配合模型能力开关 UI
caps, _ := client.GetModelCapabilities(ctx, modelID)
if caps.SupportsDeepThinking {
    req.Thinking = acosmi.NewThinkingConfig(acosmi.ThinkingMax)
}
```

### 4.4 权益管理

> scope: `ai`

```go
// 聚合余额
balance, _ := client.GetBalance(ctx)
fmt.Printf("Token: %d/%d 剩余%d | 调用: %d/%d\n",
    balance.TotalTokenUsed, balance.TotalTokenQuota, balance.TotalTokenRemaining,
    balance.TotalCallUsed, balance.TotalCallQuota)

// 带明细
detail, _ := client.GetBalanceDetail(ctx)
for _, e := range detail.Entitlements {
    fmt.Printf("  [%s] %s: %d/%d token\n", e.Type, e.Status, e.TokenUsed, e.TokenQuota)
}

// 列表 (ACTIVE / EXPIRED / "")
active, _ := client.ListEntitlements(ctx, "ACTIVE")

// 消费记录
records, _ := client.ListConsumeRecords(ctx, 1, 20)

// 领取当月免费额度 (幂等: 已领取返回已有权益, 不重复发放)
ent, _ := client.ClaimMonthlyFree(ctx)
```

### 4.5 流量包商城

> scope: `ai`

```go
packages, _ := client.ListTokenPackages(ctx)               // 浏览
pkg, _ := client.GetTokenPackageDetail(ctx, "pkg-id")      // 详情
order, _ := client.BuyTokenPackage(ctx, "pkg-id", &acosmi.PayPayload{PayMethod: "alipay"}) // 下单
status, _ := client.GetOrderStatus(ctx, "order-id")        // 状态
orders, _ := client.ListMyOrders(ctx)                       // 我的订单

// 轮询支付状态至终态 (购买链路典型用法)
// pollInterval <= 0 时默认 2s; 成功返回 (status, nil); 终态失败返回 (*OrderTerminalError)
ctx2, cancel := context.WithTimeout(ctx, 5*time.Minute)
defer cancel()
finalStatus, err := client.WaitForPayment(ctx2, order.ID, 3*time.Second)
```

### 4.6 钱包

> scope: `account`

```go
stats, _ := client.GetWalletStats(ctx)           // 余额/月消费/月充值
txns, _ := client.GetWalletTransactions(ctx)      // 交易记录
```

> 金额使用 `json.Number` 防精度丢失。

### 4.7 技能商店

```go
// 公开 (无需登录)
skills, _ := client.BrowseSkillStore(ctx, acosmi.SkillStoreQuery{Category: "ACTION", Keyword: "翻译"})
resp, _ := client.BrowseSkills(ctx, 1, 20, "ACTION", "", "", "")         // 分页完整
listResp, _ := client.BrowseSkillsList(ctx, 1, 20, "", "搜索", "", "")   // 分页轻量 (体积 -90%)
skill, _ := client.GetSkillDetail(ctx, "skill-id")                       // 详情
skill, _ := client.ResolveSkill(ctx, "translate-api")                    // 按 key 查
zipData, filename, _ := client.DownloadSkill(ctx, "skill-id")            // 下载 (匿名 2次/小时)

// 需登录
installed, _ := client.InstallSkill(ctx, "skill-id")
uploaded, _ := client.UploadSkill(ctx, zipData, "TENANT", "PUBLIC_INTENT")
summary, _ := client.GetSkillSummary(ctx)

// 认证 (scope: skills)
client.CertifySkill(ctx, "skill-id")                       // 触发
cert, _ := client.GetCertificationStatus(ctx, "skill-id")  // 查询

// AI 生成 (scope: skills)
result, _ := client.GenerateSkill(ctx, acosmi.GenerateSkillRequest{
    Purpose: "将中文翻译成英文", Category: "ACTION",
})
optimized, _ := client.OptimizeSkill(ctx, acosmi.OptimizeSkillRequest{
    SkillName: "translate-api", Aspects: []string{"accuracy"},
})
client.ValidateSkill(ctx, "translate-api")                  // 校验技能定义

// 统一工具
tools, _ := client.ListTools(ctx)   // 技能 + 插件合并视图
tool, _ := client.GetTool(ctx, "tool-id")
```

### 4.8 WebSocket 实时推送

```go
client.Connect(ctx, acosmi.WSConfig{
    Topics:       []string{"balance", "skills", "system"},
    OnEvent:      func(e acosmi.WSEvent) { fmt.Printf("[%s] %s\n", e.Type, string(e.Data)) },
    OnConnect:    func() { fmt.Println("已连接") },
    OnDisconnect: func(err error) { fmt.Printf("断开: %v\n", err) },
    ReconnectMin: 2 * time.Second,  // 默认 2s
    ReconnectMax: 60 * time.Second, // 默认 60s
})
defer client.Disconnect()

client.IsConnected() // 检查状态
```

自动断线重连 (指数退避 2s→60s)，重连后自动重新订阅。30s 握手超时。

#### system 通知类型 (13 种)

| 类型 | 触发场景 |
|------|---------|
| `task_done` | 异步任务完成 |
| `task_confirm` | 任务计划待确认 |
| `invite_success` | 邀请的好友注册 |
| `commission` | 佣金到账 |
| `register` | 新用户注册欢迎 |
| `entitlement` | 管理员发放权益 |
| `entitlement_exp` | 权益 7 天内到期 |
| `purchase` | 订单支付完成 |
| `tk_alert` | 余额低于 100 TK |
| `withdraw` | 提现打款成功 |
| `reg_bonus` | 注册奖励发放 |
| `claim_monthly` | 月度免费 Token 领取 |
| `unclaimed_reminder` | 当月未领取提醒 |

#### 通知事件解析

```go
// 从 WSEvent 中解析通知 (返回 nil 表示非通知事件)
if n := acosmi.ParseNotificationEvent(ev); n != nil {
    fmt.Printf("新通知: [%s] %s — %s\n", n.Type, n.Title, n.Content)
}
```

### 4.9 通知管理

```go
// 查询通知 (分页 + 类型过滤)
list, _ := client.ListNotifications(ctx, 1, 20, "")           // 全部
list, _ := client.ListNotifications(ctx, 1, 20, "billing")    // 仅账单类
fmt.Printf("共 %d 条, 未读 %d\n", list.Total, list.UnreadCount)

// 获取未读数 (轻量)
count, _ := client.GetUnreadCount(ctx)

// 标记已读
client.MarkNotificationRead(ctx, "notif-id-xxx")  // 单条
client.MarkAllNotificationsRead(ctx)               // 全部

// 删除
client.DeleteNotification(ctx, "notif-id-xxx")
```

### 4.10 设备注册 (推送通知)

```go
// 注册推送 Token (FCM/APNs/鸿蒙推送)
client.RegisterDevice(ctx, acosmi.DeviceRegistration{
    Platform:   "android",  // "android" | "ios" | "harmony"
    Token:      "fcm-token-xxx",
    AppVersion: "2.0.0",
})

// 注销设备 (登出时调用)
client.UnregisterDevice(ctx, "fcm-token-xxx")
```

### 4.11 通知偏好

```go
// 查询用户偏好
prefs, _ := client.ListNotificationPreferences(ctx)
for _, p := range prefs {
    fmt.Printf("%s: 站内=%v 邮件=%v 短信=%v 推送=%v\n",
        p.TypeCode, p.ChannelInApp, p.ChannelEmail, p.ChannelSMS, p.ChannelPush)
}

// 更新偏好 (关闭短信通知)
client.UpdateNotificationPreference(ctx, "tk_alert", acosmi.NotificationPreference{
    TypeCode:     "tk_alert",
    ChannelInApp: true,
    ChannelEmail: true,
    ChannelSMS:   false,
    ChannelPush:  true,
})
```

---

## 5. CLI 命令手册

```
crabclaw-skill [--server URL] [--json] <命令> [参数]
```

### 认证

| 命令 | 说明 | 需登录 |
|------|------|:------:|
| `login [--force]` | OAuth 浏览器授权 | - |
| `logout` | 吊销 + 清除本地凭证 | - |
| `whoami` | 登录状态/过期时间/范围 | - |
| `version` | 版本号和构建时间 | - |

### 配置

配置文件: `~/.acosmi/cli-config.json`

```bash
crabclaw-skill config show                    # 查看
crabclaw-skill config set server https://acosmi.com # 修改 (默认; 国际站用 https://acosmi.ai)
crabclaw-skill config set skilldir ~/my-skills
crabclaw-skill config reset                   # 重置
```

环境变量 `ACOSMI_SERVER_URL` 优先级高于配置文件。

### 技能操作

| 命令 | 说明 | 需登录 |
|------|------|:------:|
| `search <关键词> [--category --tag --source --page --page-size]` | 搜索技能 | 否 |
| `list` | 已安装技能 | 是 |
| `info <key>` | 技能详情 | 否 |
| `download <key> [-o 路径]` | 下载 ZIP (匿名 2次/时) | 否 |
| `install <key> [--local-only --dir --force]` | 安装技能 | 视参数 |
| `upload <ZIP> [--public --certify]` | 上传技能 | 是 |
| `generate "<描述>" [--category --language --save]` | AI 生成技能 | 是 |
| `certify <key>` | 触发认证流水线 | 是 |

**示例**:
```bash
crabclaw-skill search "翻译" --category ACTION
crabclaw-skill install translate-api --force
crabclaw-skill upload ./my-skill.zip --public --certify
crabclaw-skill generate "网页截图工具" --save screenshot.zip
```

---

## 6. 数据类型参考

### OAuth

```go
type ServerMetadata struct {
    Issuer, AuthorizationEndpoint, TokenEndpoint string
    RevocationEndpoint, RegistrationEndpoint     string
    ScopesSupported []string
}

type TokenSet struct {
    AccessToken, RefreshToken string
    ExpiresAt                 time.Time
    Scope, ClientID, ServerURL string
}
func (t *TokenSet) IsExpired() bool // 过期前 30s 即返回 true
```

### 模型

```go
type ManagedModel struct {
    ID, Name, Provider, ModelID string
    MaxTokens, ContextWindow    int
    IsEnabled, IsDefault        bool
    PricePerMTok                float64
    Capabilities                ModelCapabilities
    SupportedFormats            []string // v0.10.0: ["anthropic","openai"], 上游可选
    PreferredFormat             string   // v0.10.0: "anthropic" | "openai", 空则取 SupportedFormats[0]
}

type ModelCapabilities struct {
    // 思考能力
    SupportsThinking, SupportsAdaptiveThinking, SupportsISP       bool
    // 工具与搜索
    SupportsWebSearch, SupportsToolSearch, SupportsStructuredOutput bool
    // 推理控制
    SupportsEffort, SupportsMaxEffort, SupportsFastMode            bool
    SupportsAutoMode, SupportsDeepThinking                         bool // v0.9.0: 深度思考 (Opus 4.6)
    // 上下文与缓存
    Supports1MContext, SupportsPromptCache, SupportsCacheEditing   bool
    // 输出控制
    SupportsTokenEfficient, SupportsRedactThinking                 bool
    // Token 上限
    MaxInputTokens, MaxOutputTokens int
}
```

### Chat

```go
type ChatMessage struct {
    Role    string `json:"role"`    // system | user | assistant
    Content string `json:"content"`
}

type ChatRequest struct {
    // 基础字段
    Messages  []ChatMessage `json:"messages"`
    Stream    bool          `json:"stream,omitempty"`
    MaxTokens int           `json:"max_tokens,omitempty"`

    // 扩展字段 (json:"-"，通过 buildChatRequest 序列化)
    RawMessages  interface{}            // 多模态, 非 nil 时优先于 Messages
    System       interface{}            // 系统提示
    Tools        interface{}            // 标准工具定义
    Temperature  *float64
    Thinking     *ThinkingConfig
    Metadata     map[string]string
    Betas        []string               // 显式 beta (自动合并去重)
    ServerTools  []ServerTool
    Speed        string                 // "" | "fast"
    Effort       *EffortConfig
    OutputConfig *OutputConfig
    ExtraBody    map[string]interface{} // 透传任意字段
}

// v0.4.1+ ChatResponse 统一为 Anthropic content block 格式
// (OpenAI 兼容厂商由 OpenAIAdapter 在响应侧转换，消费方无需区分)
type ChatResponse struct {
    ID         string             // Anthropic message ID
    Type       string             // "message"
    Model      string
    Role       string             // "assistant"
    Content    []ChatContentBlock // text / thinking / tool_use 等混合块
    StopReason string             // end_turn / tool_use / max_tokens
    Usage      ChatUsage          // 输入/输出 token + prompt cache 用量
    TokenRemaining int64 // Header X-Token-Remaining 填充，-1=未返回
    CallRemaining  int   // Header X-Call-Remaining 填充，-1=未返回
}

type ChatContentBlock struct {
    Type       string          // text | thinking | redacted_thinking | tool_use | tool_result |
                               //  server_tool_use | mcp_tool_use | mcp_tool_result
    Text       string          // text 块内容
    Thinking   string          // thinking 块内容
    Signature  string          // thinking 块 — Anthropic 签名 (后续请求需回传)
    Data       string          // redacted_thinking — base64 审查后的思考
    Citations  interface{}     // text — web_search 引用
    ID         string          // tool_use / server_tool_use block ID
    Name       string          // tool_use function name
    Input      json.RawMessage // tool_use 参数
    ServerName string          // server/mcp tool — 服务端工具来源
    Caller     interface{}     // mcp_tool_use — 调用者上下文
    ToolUseID  string          // tool_result — 关联 tool_use
    Content    interface{}     // tool_result — 工具返回
    IsError    *bool           // tool_result
}

type ChatUsage struct {
    InputTokens        int
    OutputTokens       int
    CacheCreationInput int // prompt cache 创建 token
    CacheReadInput     int // prompt cache 读取 token
}

type AnthropicResponse struct {  // /anthropic (Anthropic 格式)
    ID           string
    Type         string                  // "message"
    Role         string                  // "assistant"
    Content      []AnthropicContentBlock
    Model        string
    StopReason   string
    StopSequence *string                 // 触发的停止序列 (可 nil)
    Usage        AnthropicUsage
}
type AnthropicContentBlock struct {
    Type       string          // text / thinking / redacted_thinking / tool_use / tool_result / server_tool_use / mcp_tool_use / mcp_tool_result
    Text       string
    ID         string          // tool_use / server_tool_use / mcp_tool_use block ID
    Name       string          // tool_use function name
    Input      json.RawMessage // tool_use arguments
    Thinking   string          // thinking block content
    Citations  interface{}     // text — web_search 搜索引用
    Signature  string          // thinking — Anthropic 签名 (后续请求需回传)
    Data       string          // redacted_thinking — base64 编码内容
    ServerName string          // server_tool_use / mcp_tool_use / mcp_tool_result — 服务端工具来源
    Caller     interface{}     // mcp_tool_use — MCP 调用者上下文
    ToolUseID  string          // tool_result / mcp_tool_result — 关联的 tool_use block ID
    Content    interface{}     // tool_result / mcp_tool_result — 工具返回内容
    IsError    *bool           // tool_result / mcp_tool_result — 是否报错
}
type AnthropicUsage struct {
    InputTokens              int
    OutputTokens             int
    CacheCreationInputTokens int // prompt cache 创建 token
    CacheReadInputTokens     int // prompt cache 读取 token
}

// 辅助方法:
// resp.TextContent()      → 提取所有 text 块文本
// resp.ThinkingContent()  → 提取所有 thinking 块文本
// resp.ToolUseBlocks()    → 返回所有 tool_use 块

type StreamEvent struct {
    Event string // started | settled | pending_settle | failed | "" (数据块)
    Data  string
}

type StreamSettlement struct {
    RequestID, ConsumeStatus           string
    InputTokens, OutputTokens, TotalTokens int
    TokenRemaining int64 // -1=未返回
    CallRemaining  int
}
func ParseSettlement(ev StreamEvent) *StreamSettlement
```

### Chat 扩展类型

```go
type ThinkingConfig struct {
    Type         string // "adaptive" | "enabled" | "disabled"
    BudgetTokens int    // 仅 type="enabled"，旧模型回退用
    Level        string // v0.9.0: "off" | "high" | "max"
                        //  设置后 SDK 自动组装 thinking + effort + max_tokens
                        //  为空字符串时 passthrough (v0.8.0 兼容)
    Display      string // "" (完整) | "summary" | "none"
}

// v0.9.0 Thinking Level 常量
const (
    ThinkingOff  = "off"  // 关闭思考
    ThinkingHigh = "high" // 标准: thinking=adaptive + effort=high + maxTokens≥32K
    ThinkingMax  = "max"  // 深度: thinking=adaptive + effort=max + maxTokens=模型上限
)

// v0.9.0 组装用常量
const (
    ThinkingHighMinMaxTokens     = 32_000  // high 级最低 maxTokens
    ThinkingMaxFallbackMaxTokens = 128_000 // max 级 fallback (caps.MaxOutputTokens 不可用时)
)

// NewThinkingConfig 便捷构造 (推荐入口)
// off → {Type:"disabled"}; high/max → {Type:"adaptive", Level:level}
func NewThinkingConfig(level string) *ThinkingConfig

type ServerTool struct { Type, Name string; Config map[string]interface{} }
type WebSearchConfig struct {
    MaxUses int; AllowedDomains, BlockedDomains []string; UserLocation *GeoLoc
}
type GeoLoc struct { Country, City string }
type EffortConfig struct { Level string }  // low | medium | high | max
type OutputConfig struct { Format string; Schema interface{} }

const ServerToolTypeWebSearch = "web_search_20250305"
func NewWebSearchTool(cfg *WebSearchConfig) (ServerTool, error) // AllowedDomains ⊕ BlockedDomains 互斥
func (c *Client) GetModelCapabilities(ctx, modelID) (*ModelCapabilities, error) // 缓存 5min
```

### 搜索来源

```go
type WebSearchSource struct { Title, URL, Snippet string }
type SourcesEvent struct { Sources []WebSearchSource; SessionID string }
func ParseSourcesEvent(ev StreamEvent) *SourcesEvent
```

### 权益

```go
type EntitlementBalance struct {
    TotalTokenQuota, TotalTokenUsed, TotalTokenRemaining int64
    TotalCallQuota, TotalCallUsed, TotalCallRemaining    int
    ActiveEntitlements int
}

type EntitlementItem struct {
    ID, Type, Status string // Type: REG_BONUS|FREE_TRIAL|TOKEN_PKG|MONTHLY  Status: active|expired|exhausted
    TokenQuota, TokenUsed, TokenRemaining int64
    CallQuota, CallUsed, CallRemaining    int
    ExpiresAt  *string
    SourceID   string // 来源 ID
    SourceType string // 来源类型
    Remark     string
    CreatedAt  string
}

type BalanceDetail struct {
    TotalTokenQuota, TotalTokenUsed, TotalTokenRemaining int64
    TotalCallQuota, TotalCallUsed, TotalCallRemaining    int
    ActiveEntitlements int
    Entitlements []EntitlementItem
}

type ConsumeRecord struct {
    ID, EntitlementID, RequestID, ModelID string
    TokensConsumed int64; Status, CreatedAt string
}
type ConsumeRecordPage struct { Records []ConsumeRecord; Total int64; Page, PageSize int }
```

### 商城 / 钱包

```go
type TokenPackage struct {
    ID, Name, Description string; TokenQuota int64; CallQuota int
    Price json.Number; ValidDays int; IsEnabled bool; SortOrder int
}

type Order struct {
    ID, PackageID, PackageName string; Amount json.Number
    Status, PayURL, CreatedAt string // pending|paid|expired|cancelled
}

type OrderStatus struct { OrderID, Status string }

type PayPayload struct { PayMethod string } // alipay | wechat 等

type WalletStats struct {
    Balance, MonthlyConsumption, MonthlyRecharge json.Number
    TransactionCount int
}
type Transaction struct { ID, Type string; Amount json.Number; Remark, CreatedAt string }

func (c *Client) WaitForPayment(ctx, orderID, pollInterval) (*OrderStatus, error)
// 成功→(status, nil); 非成功终态→(*OrderTerminalError); 超时→ctx.Err()
```

### 技能

```go
type SkillStoreItem struct {
    ID, PluginID, Key, Name, Description, Icon, Category string
    InputSchema, OutputSchema, Version, Author, PublisherID string
    Readme, Scope, Status, Visibility, CertificationStatus string
    PluginName, PluginIcon string
    Tags []string; DownloadCount, TotalCalls, AvgDurationMs int64
    SecurityScore int; SecurityLevel string; SuccessRate float64
    IsEnabled, IsPublished bool; Timeout, RetryCount, RetryDelay int
    Source, UpdatedAt string
}

type SkillStoreListItem struct {
    ID, Key, Name, Description, Icon, Category, Version, Author string
    CertificationStatus, Visibility, Source, UpdatedAt string
    Tags []string; DownloadCount int64
}

type SkillStoreQuery struct { Category, Keyword, Tag string } // BrowseSkillStore 便捷查询参数

type SkillBrowseResponse struct { Items []SkillStoreItem; Total int64; Page, PageSize int }
type SkillBrowseListResponse struct { Items []SkillStoreListItem; Total int64; Page, PageSize int }

type CertificationStatus struct {
    SkillID, CertificationStatus, SecurityLevel string
    CertifiedAt *int64; SecurityScore int; Report interface{}
}

type SkillSummary struct {
    Installed, Created, Total, StoreAvailable int64
}

type GenerateSkillRequest struct {
    Purpose string; Examples []string; InputHints, OutputHints, Category, Language string
}
type GenerateSkillResult struct {
    SkillName, SkillKey, Description, SkillMd, InputSchema, OutputSchema string
    Readme, Category string; Tags, TestCases []string; Timeout int
}
type OptimizeSkillRequest struct {
    SkillName, Description, InputSchema, OutputSchema, Readme string
    Aspects []string
}
type OptimizeSkillResult struct {
    OptimizedSkill GenerateSkillResult; Changes []string; Score int
}
```

### 工具

```go
type ToolView struct {
    ID, Key, Name, Description, Icon, Category string
    InputSchema, OutputSchema string; Timeout int; IsEnabled bool
    Provider *ToolProvider
}
type ToolProvider struct {
    ID, Name, Icon string
    SourceType string // NATIVE|PROMPT|MCP|WORKFLOW|HTTP|ENGINE
    MCPEndpoint string; IsEnabled bool
}
```

### WebSocket

```go
type WSConfig struct {
    OnEvent func(WSEvent); OnConnect func(); OnDisconnect func(error)
    Topics []string
    ReconnectMin, ReconnectMax time.Duration; AutoReconnect *bool
}
type WSEvent struct {
    Type, Topic string; Data json.RawMessage
    ConnID, Timestamp, Message string
}
```

### 通知

```go
type Notification struct {
    ID, Title, Content, Type string   // Type: system|billing|security|task|commission|entitlement
    IsRead bool; CreatedAt string
}
type NotificationList struct {
    List []Notification; UnreadCount, Total int64; Page, PageSize int
}
type NotificationUnreadCount struct { UnreadCount int64 }
type NotificationPreference struct {
    TypeCode string
    ChannelInApp, ChannelEmail, ChannelSMS, ChannelPush bool
}
type DeviceRegistration struct {
    Platform string   // android | ios | harmony
    Token, AppVersion string
}
func ParseNotificationEvent(ev WSEvent) *Notification // 返回 nil 表示非通知
```

### 错误

```go
type RateLimitError struct { Message, RetryAfter, Raw string }

// BusinessError: API 业务层错误 (HTTP 200 但 code != 0, tk-dist 代理透传 yudao 响应)
type BusinessError struct { Code int; Message string }

// OrderTerminalError: 订单到达非成功终态 (FAILED/CANCELLED/CLOSED/EXPIRED/REFUNDED)
// WaitForPayment 在终态非成功时返回
type OrderTerminalError struct { OrderID, Status string }

// HTTP 错误统一解析 (parseHTTPError):
// Anthropic: {"type":"error","error":{"type":"...","message":"..."}} → "HTTP 400: [invalid_request_error] ..."
// OpenAI:    {"error":{"message":"..."}}                            → "HTTP 400: ..."
// 其他:      原始响应体                                               → "HTTP 400: {raw body}"
// 所有 Chat/ChatMessages/ChatStream 等方法均使用此统一解析

// 类型断言示例
var rateErr *acosmi.RateLimitError
if errors.As(err, &rateErr) { /* 限流 */ }
var bizErr *acosmi.BusinessError
if errors.As(err, &bizErr) { fmt.Printf("业务错误 code=%d: %s\n", bizErr.Code, bizErr.Message) }
var termErr *acosmi.OrderTerminalError
if errors.As(err, &termErr) { fmt.Printf("订单 %s 终态: %s\n", termErr.OrderID, termErr.Status) }
```

### 登录事件 (LoginWithHandler)

```go
type LoginEventType string // "auth_url" | "complete" | "error"
const (
    EventAuthURL  LoginEventType = "auth_url"
    EventComplete LoginEventType = "complete"
    EventError    LoginEventType = "error"
)

type LoginEvent struct {
    Type    LoginEventType
    URL     string       // EventAuthURL 时填充
    Error   string       // EventError 时填充
    ErrCode LoginErrCode // EventError 时填充
}

type LoginErrCode string // discovery_failed|registration_failed|browser_open_failed|auth_denied|auth_timeout|token_exchange_failed|ssl_proxy_detected

type LoginOption func(*loginConfig)
func WithSkipBrowser() LoginOption        // 跳过自动打开浏览器
func WithLoginHint(hint string) LoginOption   // SSO email 预填 (login_hint)
func WithLoginMethod(method string) LoginOption // 登录方式 (如 "sso")
func WithOrgUUID(uuid string) LoginOption     // 强制组织登录
func WithExpiresIn(seconds int) LoginOption   // 自定义 token 有效期 (秒)
```

### 通用响应

```go
type APIResponse[T any] struct {
    Code int; Message, Msg string; Data T // Msg: yudao 兼容
}
func (r *APIResponse[T]) GetMessage() string // 优先 Message, 降级 Msg
```

---

## 7. 完整示例

```go
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
    // 默认 https://acosmi.com (大陆), 国际站显式传 https://acosmi.ai
    serverURL := os.Getenv("ACOSMI_SERVER_URL")
    if serverURL == "" {
        serverURL = "https://acosmi.com"
    }

    client, err := acosmi.NewClient(acosmi.Config{ServerURL: serverURL})
    if err != nil { log.Fatal(err) }

    ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
    defer cancel()

    // 登录
    if !client.IsAuthorized() {
        if err := client.Login(ctx, "SDK Demo", acosmi.AllScopes()); err != nil {
            log.Fatal(err)
        }
    }

    // 余额
    balance, _ := client.GetBalance(ctx)
    fmt.Printf("Token: %d/%d (剩余 %d)\n",
        balance.TotalTokenUsed, balance.TotalTokenQuota, balance.TotalTokenRemaining)

    // 钱包
    if w, err := client.GetWalletStats(ctx); err == nil {
        fmt.Printf("钱包: %s 元\n", w.Balance)
    }

    // 模型
    models, _ := client.ListModels(ctx)
    if len(models) == 0 { fmt.Println("无可用模型"); return }

    // WebSocket
    client.Connect(ctx, acosmi.WSConfig{
        Topics:  []string{"balance", "skills"},
        OnEvent: func(e acosmi.WSEvent) { fmt.Printf("[WS] %s\n", e.Type) },
    })
    defer client.Disconnect()

    // 流式聊天 (ChatStreamWithUsage: 内容/搜索来源/结算/错误 4 channel)
    fmt.Printf("使用 %s 对话:\n", models[0].Name)
    contentCh, sourcesCh, settleCh, errCh := client.ChatStreamWithUsage(ctx, models[0].ID, acosmi.ChatRequest{
        Messages:  []acosmi.ChatMessage{{Role: "user", Content: "用一句话介绍你自己"}},
        MaxTokens: 256,
    })

    go func() {
        for src := range sourcesCh {
            for _, s := range src.Sources { fmt.Printf("  [来源] %s %s\n", s.Title, s.URL) }
        }
    }()

    for event := range contentCh {
        // Anthropic 格式优先, 失败时回退 OpenAI 格式 (详见 §4.3 ChatStream 说明)
        var ant struct {
            Type  string `json:"type"`
            Delta struct{ Type, Text string } `json:"delta"`
        }
        if json.Unmarshal([]byte(event.Data), &ant) == nil &&
            ant.Type == "content_block_delta" && ant.Delta.Text != "" {
            fmt.Print(ant.Delta.Text)
            continue
        }
        var oai struct {
            Choices []struct{ Delta struct{ Content string `json:"content"` } `json:"delta"` } `json:"choices"`
        }
        if json.Unmarshal([]byte(event.Data), &oai) == nil && len(oai.Choices) > 0 {
            fmt.Print(oai.Choices[0].Delta.Content)
        }
    }
    fmt.Println()
    if settle, ok := <-settleCh; ok {
        fmt.Printf("消耗: %d token, 剩余: %d\n", settle.TotalTokens, settle.TokenRemaining)
    }
    if err := <-errCh; err != nil { log.Fatal(err) }

    // ── Anthropic 原生格式调用 ──
    fmt.Println("\n=== Anthropic 原生格式 ===")
    anthropicResp, err := client.ChatMessages(ctx, "claude-opus-4-6", acosmi.ChatRequest{
        RawMessages: []map[string]interface{}{
            {"role": "user", "content": "用一句话介绍 Go 语言"},
        },
        MaxTokens: 256,
    })
    if err != nil {
        log.Fatal(err)
    }
    fmt.Println(anthropicResp.TextContent())
    fmt.Printf("tokens: %d in / %d out\n",
        anthropicResp.Usage.InputTokens, anthropicResp.Usage.OutputTokens)
}
```

---

## 8. 安全特性

| # | 风险 | 防护 |
|---|------|------|
| 1 | appName JSON 注入 | `json.Marshal` 编码 |
| 2 | Token 无限刷新 | 强制最低 60s 有效期 |
| 3 | 超时截断 SSE | 不设全局 Timeout |
| 4 | 401 无限递归 | 单次重试限制 |
| 5 | 并发竞争 | `sync.RWMutex` |
| 6 | ZIP 边界碰撞 | 随机边界 |
| 7 | SSE 超大行 | 1MB 缓冲 |
| 8 | 错误 XSS | HTML 转义 |
| 9 | Scope 篡改 | 函数返回新切片 |
| 10 | Zip Slip | 路径穿越检查 + 去除 setuid |
| 11 | WebSocket 握手阻塞 | 30s 超时 |

Token 文件: 目录 `0700`，文件 `0600`。下载限制 50MB。

---

## 9. 项目结构

```
acosmi-sdk-go/
├── adapter.go             # ProviderAdapter 接口 + 注册表 + getAdapter()
├── adapter_anthropic.go   # Anthropic 格式 adapter (betas/ServerTools/ExtraBody/resolveThinkingLevel)
├── adapter_openai.go      # OpenAI 兼容格式 adapter + SSE↔Anthropic 事件转换器
├── client.go              # 统一 API 客户端 + buildChatRequest (委托 adapter)
├── client_test.go         # 客户端单元测试
├── thinking_test.go       # Thinking Level 自动组装测试 (v0.9.0)
├── auth.go                # OAuth 2.1 PKCE + LoginOption + LoginEvent
├── types.go               # 数据类型 (Anthropic/OpenAI 响应 + Thinking Level 常量)
├── betas.go               # Beta Header 自动组装 (10 项，仅 Anthropic adapter 调用)
├── store.go               # Token 持久化 (文件实现 + TokenStore 接口)
├── scopes.go              # Scope 常量 (3 分组 + 10 项 Deprecated 细粒度)
├── ws.go                  # WebSocket (自动重连 + 指数退避)
├── cmd/crabclawskill/     # CLI (13 个子命令)
├── npm/                   # NPM 包装 (@acosmi/crabclaw-skill)
├── docs/guide.md          # 本手册
└── example/main.go        # 完整示例
```

---

## 10. 构建与发布

```bash
make build      # 当前平台 → bin/crabclaw-skill
make build-all  # 全平台 → dist/crabclaw-skill-{os}-{arch}
make install    # → $GOPATH/bin
```

| 平台 | 架构 |
|------|------|
| macOS | arm64 / amd64 |
| Linux | arm64 / amd64 |
| Windows | amd64 |

版本注入: `git tag v0.x.0 && make build`。NPM 发布: `cd npm && npm publish --access public`。

---

## 11. 常见问题

**Q: Token 过期了？** — SDK 自动刷新。Refresh Token 过期 (7 天不活动) 需 `login --force`。

**Q: 匿名下载限流？** — 登录后无限制。

**Q: CI/CD 使用？** — 本地登录 → 将 `~/.acosmi/tokens.json` 作为 CI Secret 写入同路径。

**Q: WebSocket 重连？** — 检查地址/token/防火墙。可设 `AutoReconnect: &false` 禁用。

**Q: SDK 线程安全？** — 是，`sync.RWMutex` 保护所有共享状态。

---

## 12. 版本记录

### v0.11.0 (2026-04-22) — Sanitize 包 + StreamEvent Block 元数据 + in-band Ephemeral

全新增字段 / 枚举, 零值等价 v0.10.0 行为, 未接入的消费者无感。

- **feat(sanitize)**: 新增 `github.com/acosmi/acosmi-sdk-go/sanitize` 子包
  - `BlockType` 枚举 (16 种 content block 类型, 覆盖 Anthropic 请求/响应 + ephemeral)
  - `DeltaType` 枚举 (text_delta / input_json_delta / thinking_delta / signature_delta / citations_delta)
  - `MinimalSanitizeConfig` — 底线防御配置 (base64 体积上限 + history 深度上限 + `PermanentDenyBlocks` 黑名单)
  - `Sanitize(messages, cfg) ([]any, error)` — 早失败式体积校验 + deny-list 剥除 + tool_use_id 联动剥 tool_result
  - `StripEphemeral(messages) []any` — 按 in-band `acosmi_ephemeral:true` 标记剥历史, 联动剥对应 tool_result
  - `EphemeralMarkerField = "acosmi_ephemeral"` — 网关/消费者共享的标记字段名常量
- **feat(client)**: 两个 Client 级钩子, 未配置时零开销
  - `SetDefensiveSanitize(cfg sanitize.MinimalSanitizeConfig)` — 每次 Chat/ChatStream/ChatMessages* 请求前执行
  - `SetAutoStripEphemeralHistory(on bool)` — 开启后自动扫 `RawMessages` 剥 ephemeral block
- **feat(types)**: `StreamEvent` 新增 3 个 `json:"-"` 字段 (零值兼容)
  - `BlockIndex int` — 对齐 Anthropic `content_block_*` 的 index
  - `BlockType string` — 从 `content_block_start.content_block.type` 解出, delta/stop 查 map 继承
  - `Ephemeral bool` — 从 `content_block_start.content_block.acosmi_ephemeral` in-band 字段解出
- **feat(stream)**: Anthropic SSE 循环维护 `blockTypeMap[index] → meta`, 单 goroutine 无锁
  - 零额外缓冲: in-band 标记随 `content_block_start` JSON 同步到达, 不需要独立 SSE meta 事件, 无顺序依赖
  - 零延迟: 标记解析与原事件透传同一 tick
  - `content_block_stop` 后删表项, 长流无累积
- **compat**: 所有新字段零值等价旧行为; `Sanitize` 未调用时 `buildChatRequest` 完全跳过新路径; v0.10.0 消费者零改动升级
- **test**: +24 单测 + 1 fuzz, 2.5M 次无 panic
  - `sanitize/defensive_test.go` — S-1/S-2/S-3/S-9 (体积/deny/深度 + data URL 前缀兜底 + URL 源跳过)
  - `sanitize/history_test.go` — StripEphemeral + H-2 tool_use_id 联动 + 零拷贝 + malformed 不 panic
  - `sanitize/fuzz_test.go` — S-15 喂任意 JSON, 覆盖 255 个 interesting input
  - `stream_meta_test.go` — S-4/S-5 (index/type/ephemeral 映射, delta/stop 继承, 非 block 事件不污染)
  - `sanitize_bridge_test.go` — Messages/RawMessages 两分支深度校验 + struct 切片归一化 + 零配置 no-op

#### 使用示例

```go
import "github.com/acosmi/acosmi-sdk-go/sanitize"

client, _ := acosmi.NewClient(acosmi.Config{ServerURL: "https://acosmi.com"})

// 启用底线防御 (可选, 按需配置)
client.SetDefensiveSanitize(sanitize.MinimalSanitizeConfig{
    MaxImageBytes:    5 * 1024 * 1024,   // 图片 ≤5MB 早失败
    MaxVideoBytes:    50 * 1024 * 1024,
    MaxPDFBytes:      10 * 1024 * 1024,
    MaxMessagesTurns: 100,
    // PermanentDenyBlocks 按需追加, 默认留空 (由网关决定)
})

// 开启自动剥离 ephemeral 历史 (网关 in-band 标记的 thinking / server_tool_use 等)
client.SetAutoStripEphemeralHistory(true)

// 流式消费示例: 使用 BlockType / Ephemeral 过滤 UI 展示
for ev := range eventCh {
    if ev.BlockType == string(sanitize.BlockThinking) && ev.Ephemeral {
        // thinking 块, UI 可隐藏或折叠, 下一轮 SDK 自动不回传
        continue
    }
    // ... 其他展示逻辑
}
```

#### 与网关协议约定 (in-band ephemeral)

网关在 `content_block_start` 的 JSON payload 里直接注入 `acosmi_ephemeral: true` 字段, 例如:

```
event: content_block_start
data: {"type":"content_block_start","index":0,"content_block":{"type":"thinking","acosmi_ephemeral":true}}
```

选择 in-band 而非独立 `event: acosmi_meta` 事件的理由: **零缓冲 / 零顺序依赖 / 零延迟 / history 剥离天然可做** (消费者 history 中的 block 自带标记, SDK 无需另外记忆)。

### v0.10.0 (2026-04-22) — Capability-driven Adapter 路由 ⚠️ 破坏性

- **fix(adapter)**: 根因修复 CrabCode TUI 在 DashScope/Zhipu/DeepSeek 等 provider 使用 WebSearch + ToolSearch 时报 `HTTP 400: unknown variant tool_reference` 的问题
  - 根因: v0.5.0 `getAdapter(provider string)` 按 provider 名硬编码, 非 `{anthropic, acosmi}` 的 provider 永远走 OpenAIAdapter → `/chat` 端点, 但 `tool_reference` 等 Anthropic 专属 content block 无法被 Rust gateway OpenAI 校验器接受
- **feat(types)**: `ManagedModel` 新增两个字段 (上游 Gateway 在 `/models` 响应中填充)
  - `SupportedFormats []string` — 上游启用的请求格式列表 (`"anthropic"` / `"openai"`)
  - `PreferredFormat string` — 推荐客户端优先使用的格式
  - 两字段均 `omitempty`, 旧 Gateway 未填时 SDK 回落 provider 硬编码 (向后兼容)
- **feat(adapter)**: 新增 `getAdapterForModel(m ManagedModel)` 替代 `getAdapter(provider string)`
  - 四层优先级: `PreferredFormat` → `SupportedFormats` → provider 硬编码回落
  - 大小写不敏感 (`"Anthropic"` / `"ANTHROPIC"` 均有效)
- **refactor(client)**: `buildChatRequest` / `ChatMessages` 调用点改读完整 `ManagedModel` (新 `getCachedModel`), 废弃 `getModelProvider`
- **breaking**: DashScope / Zhipu / DeepSeek 三家 provider 的模型, 如上游返回 `preferred_format: "anthropic"`, 请求将从 `/chat` 切换到 `/anthropic` 端点。若需保留旧行为, Gateway 侧把 `SupportedFormats` 限定为 `["openai"]` 或 `PreferredFormat: "openai"` 即可显式覆盖
- **compat**: 旧版 SDK 读不到新字段, 继续走 `/chat` — 向后兼容未破坏
- **test**: `adapter_test.go` 覆盖 8 个用例 — PreferredFormat 覆盖硬编码 / SupportedFormats 多值选择 / 大小写 / 空值回落

### v0.9.0 — Thinking Level 自动组装

- **feat(thinking)**: 新增三档语义化思考级别 API
  - 常量: `ThinkingOff` / `ThinkingHigh` / `ThinkingMax`
  - 便捷构造: `NewThinkingConfig(level)`
  - `ThinkingConfig` 新增 `Level` 字段 (json:"level,omitempty")
  - 辅助常量: `ThinkingHighMinMaxTokens` (32K) / `ThinkingMaxFallbackMaxTokens` (128K)
- **feat(adapter)**: `resolveThinkingLevel` 在 `AnthropicAdapter.BuildRequestBody` 中自动组装
  - Level 非空时 SDK 接管 `thinking` + `effort` + `max_tokens` 三字段
  - 模型不支持 `adaptive` 时自动回退 `enabled + budget_tokens = max_tokens - 1`
  - Level 非 `off` 时自动删除 `temperature` (Anthropic API 互斥约束)
  - Level=`high`/`max` 且 `SupportsEffort` 时自动注入 `effort-2025-11-24` beta
- **feat(capabilities)**: `ModelCapabilities` 新增 `SupportsDeepThinking` (Opus 4.6 深度思考门控)
- **compat**: `Level=""` 时保持 v0.8.0 passthrough — 老代码零影响
- **test**: `thinking_test.go` 覆盖 off/high/max × adaptive/old model 共 9 个 case + betaEffort 注入 + temperature 互斥

### v0.8.0 — Thinking / Effort Passthrough 兼容基线

- `ThinkingConfig` / `EffortConfig` 保持 passthrough 序列化语义 (调用方自行组装完整字段)
- 作为 v0.9.0 Level API 的兼容基线 — `Level=""` 时仍走此路径, 老代码零影响
- (`thinking_test.go` 以 `nil thinking → v0.8.0 passthrough` case 固化此行为)

### v0.7.0 — 文档修订

- 修正开发手册中默认 `ServerURL` 的描述（`acosmi.com` 一直是代码默认, 仅文档误写为 `acosmi.ai`）
- `acosmi.com` (大陆) 与 `acosmi.ai` (国际) 端点完全兼容, 按业务区域显式传入即可

### v0.6.0 (2026-04-13) — 通知系统 + 设备注册

- **feat(notification)**: 新增 9 个通知管理方法
  - `ListNotifications` / `GetUnreadCount` — 分页查询 + 轻量未读数
  - `MarkNotificationRead` / `MarkAllNotificationsRead` — 单条/批量已读
  - `DeleteNotification` — 删除通知
  - `RegisterDevice` / `UnregisterDevice` — 推送设备 Token 注册/注销
  - `ListNotificationPreferences` / `UpdateNotificationPreference` — 通知偏好 CRUD
- **feat(types)**: 新增 6 个类型
  - `Notification` / `NotificationList` / `NotificationUnreadCount`
  - `NotificationPreference` / `DeviceRegistration`
  - `ParseNotificationEvent(WSEvent)` — WebSocket 通知事件解析辅助函数
- 所有新方法遵循 `doJSON` + `APIResponse[T]` 既有模式，完全向后兼容

### v0.5.0 (2026-04-11) — 多厂商 Provider Adapter

- **feat(adapter)**: 新增 ProviderAdapter 接口 — per-provider 路由 + 格式转换
  - `AnthropicAdapter`: `/anthropic` 端点，完整 betas/ServerTools 注入
  - `OpenAIAdapter`: `/chat` 端点，无 betas，扩展字段透传 Gateway
- `buildChatRequest` 委托 adapter，返回 `([]byte, ProviderAdapter, error)`
- `Chat()` / `ChatMessages()` / `ChatStream()` / `ChatMessagesStream()` 全部按 provider 路由
- `ChatMessages()` 拆分为 `chatMessagesAnthropic()` / `chatMessagesOpenAI()`
- 新增 `getModelProvider()` — 从模型缓存获取 provider，默认回退 "anthropic"
- 新增 `openAIStreamConverter` — OpenAI SSE → Anthropic 事件转换 (thinking/text/tool_calls)
- 新增 `parseOpenAIResponseToAnthropic()` — OpenAI 响应 → AnthropicResponse
- 新增 10 个 OpenAI 响应类型: `OpenAIChatResponse` / `OpenAIChatChoice` / `OpenAIChatMessage` / `OpenAIToolCall` / `OpenAIFunctionCall` / `OpenAIUsage` / `OpenAIStreamChunk` / `OpenAIStreamChoice` / `OpenAIStreamDelta` / `OpenAIStreamToolCall`
- CrabCode `filterAnthropicModels` → `filterSupportedModels`: 添加 moonshot/volcengine
- CrabCode `chatErrorToDetail`: 新增 `invalid_request_error` 检测 (code 3003)

### v0.4.1 (2026-04-10) — 全量审计修复

- **fix(betas)**: Anthropic 端点 betas 静默丢失 — `AnthropicProxyRequest.Betas` 从 `json:"-"` 改为 `json:"betas,omitempty"` 支持 body 传递 (header 仍优先覆盖)
- 端点路由 `/messages` → `/anthropic` (区分格式，不拼接上游后缀)
- `parseHTTPError()` 统一 6 处错误解析 (兼容 Anthropic + OpenAI 错误格式)
- `AnthropicUsage` 补齐 `CacheCreationInputTokens` / `CacheReadInputTokens`
- `AnthropicResponse` 补齐 `StopSequence` 字段
- 新增 `ThinkingContent()` / `ToolUseBlocks()` 辅助方法
- Gateway: `adaptAnthropic`/`adaptPassthrough` 提取 `applyCommonFields` 消除重复
- Gateway: 流式 token 提取 OpenAI/Anthropic 分路径 + 字符串预检优化
- CGO: 5 个 Rust FFI 包添加 `//go:build cgo` + `!cgo` stub
- **fix(endpoint)**: 新增 `providerAnthropicEndpoints` 映射 — DeepSeek/DashScope/Zhipu 各自 Anthropic 端点，修复 Chat() 调用 /anthropic 时上游 404
- Gateway: `ResolveEndpoint` 增加 `anthropicFormat` 参数，Anthropic 端点表优先 + 回退警告日志

### v0.4.0 (2026-04-10) — Anthropic 原生格式支持

- 新增 `ChatMessages()` 同步调用 Anthropic 原生端点 (`POST /:id/anthropic`)
- 新增 `ChatMessagesStream()` 流式调用 Anthropic 原生端点
- 新增 `AnthropicResponse` / `AnthropicContentBlock` / `AnthropicUsage` 类型
- 新增 `AnthropicResponse.TextContent()` 便捷方法
- Anthropic 流式无 `started`/`settled`/`failed` 自定义事件，无 `[DONE]`，`message_stop` 为自然结束

### v0.3.1 (2026-04-09) — 开发手册审计修正

- 补齐遗漏 API 文档: `ClaimMonthlyFree` / `WaitForPayment` / `ValidateSkill` / `LoginWithHandler`
- 修正 `UploadSkill` scope 参数 (`"PUBLIC"` → `"TENANT"`)
- 修正 `ListEntitlements` 参数大小写 (`"active"` → `"ACTIVE"`)
- 补齐§6 遗漏类型: `BalanceDetail` / `BusinessError` / `OrderTerminalError` / `LoginEvent` / `OptimizeSkillRequest` / `SkillSummary` / `SkillBrowseResponse` 等
- 更新§7 完整示例对齐 `ChatStreamWithUsage` 4-channel API
- 修正 CLI 子命令计数 (14 → 13)

### v0.3.0 (2026-04-06) — Capabilities 驱动化 + 搜索来源

- `ModelCapabilities` 新增 `SupportsAutoMode`
- 新增 `WebSearchSource` / `SourcesEvent` / `ParseSourcesEvent`
- **Breaking**: `ChatStreamWithUsage` 返回 4 channel (新增 sourcesCh)

### v0.2.1 (2026-04-06) — 实时余额推送

- `ChatResponse` 新增 `TokenRemaining` / `CallRemaining` (Header 填充)
- 新增 `StreamSettlement` + `ParseSettlement`
- 新增 `ChatStreamWithUsage` 高级流式 API
- `Chat()` 改用 `doJSONFull` 读取余额 Header
- 审计: channel send 防泄漏 + failed 事件正确路由

### v0.2.0 (2026-04-06) — CrabCode 扩展能力

- 新增 `betas.go` — 11 项 Beta Header 自动组装
- `ChatRequest` 新增 12 个扩展字段 (全部 `json:"-"`)
- 新增 `ModelCapabilities` (16 项能力标记)
- 新增 `buildChatRequest` 内部序列化 + 模型缓存 (5min TTL)
- 新增 `LoginWithHandler` + 函数式选项 (CrabCode 适配)
- 后端: `sanitizeBetas` + `safePositiveInt` + `ManagedModelPublicResponse`
- 后端: Chat 输入校验 (betas≤20, tools≤50, messages≤500)

### v0.1.0 (2026-03-22) — 初始发布

- 合并 desktop-sdk-go + jineng-sdk-go
- 34 个公开 API + CrabClaw-Skill CLI (13 命令)
- 18 项根因修复

---

MIT License | Copyright (c) 2026 Acosmi
