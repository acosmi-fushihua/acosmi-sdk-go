# Acosmi Go SDK 开发手册

> v0.14.0 | Go 1.22+ | MIT

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

内部复用 `ListModels` 缓存 (5min TTL)。建议在应用启动或切换租户/环境后先调用一次 `ListModels()` 预热，确保模型能力与 `preferred_format` / `supported_formats` 路由信息已在本地缓存。

> **v0.13.x 破坏性变更**: `Chat` / `ChatMessages` / `ChatStream` / `ChatMessagesStream` 在模型缓存未命中时会**自动触发一次 `ListModels()` 刷新**。若刷新后仍找不到该 modelID, 返回 `*ModelNotFoundError` 而非静默回退到 Anthropic 路由 (修复 F2 根因: 原硬编码 `Provider:"anthropic"` 占位导致 non-anthropic 模型被误发到 `/anthropic` 端点)。调用方可 `errors.As(err, &mnf)` 捕获。

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

#### 流式错误结构化处理 — *StreamError (v0.14.1+)

`errCh` 收到的 error 在大多数场景是 `*acosmi.StreamError`,可用 `errors.As` 提取结构化字段做重试决策:

```go
import "errors"

if err := <-errCh; err != nil {
    var se *acosmi.StreamError
    if errors.As(err, &se) {
        // se.Code      错误分类 (gateway gwerrors.Kind 同口径)
        // se.Retryable 是否值得重试
        // se.Message   用户面友好文案 (中文)
        // se.RawError  上游原始错误 / 调试信息
        // se.Stage     发生阶段 ("provider" / "settlement")

        if se.Retryable {
            // 等 200ms-1s 后重试 (网关已做 1 次透明重试,此处再重试是双保险)
            log.Printf("retrying: %s [%s]", se.Message, se.Code)
            // ... 重试逻辑 ...
        } else {
            log.Fatalf("non-retryable: %s [%s]", se.Message, se.Code)
        }
    } else {
        // 非 StreamError 的错误 (transport / build-time / token refresh fail 等)
        log.Fatal(err)
    }
}
```

**Code 取值参考** (网关 V2 P0 起,值与 backend `gwerrors.Kind` 完全对齐):

| Code | Retryable | 含义 |
|---|---|---|
| `empty_response` | ✅ | 上游 200 + 空 body / 0 SSE chunks |
| `rate_limit` | ✅ | 上游 429 (附带 Retry-After) |
| `overloaded` | ✅ | 上游 529 / body 含 overloaded |
| `server` | ✅ | 上游 5xx |
| `upstream_timeout` | ✅ | 网关到上游超时 (ctx deadline / dial timeout) |
| `upstream_disconnect` | ✅ | EOF / ECONNRESET (网关已做 1 次透明重试,SDK 看到说明 2 次都失败) |
| `upstream_unreachable` | ❌ | DNS / TLS / dial refused |
| `upstream_malformed` | ❌ | 上游 200 但 body 解析失败 |
| `client_canceled` | ❌ | 用户主动 abort (一般 SDK 不会收到) |
| `authentication` | ❌ | 凭证错 |
| `arrearage` | ❌ | 余额不足 |
| `model_not_found` | ❌ | 模型未找到 |
| `not_found` | ❌ | 端点 404 |
| `invalid_request` | ❌ | 请求 body 格式错 |

**event 名兼容性** (v0.14.1 起):
SDK 同时识别 `event: failed` (acosmi managed-model 协议) 和 `event: error` (Anthropic 协议),两者都路由到 `errCh`,由 `parseStreamError` 统一解码三种 schema:
- acosmi 老协议: `{errorCode, stage, error: "string", message, retryable}`
- Anthropic 协议 + acosmi 私有扩展: `{type:"error", error:{type, message}, errorCode, retryable, message, stage}`
- Anthropic 标准纯净: `{type:"error", error:{type, message}}` (此时 `Code` 用 `error.type` 兜底)

> v0.14.0 及以下版本不识别 `event: error`,在 `/managed-models/<id>/anthropic` 路径上拿不到结构化错误。建议升级到 v0.14.1+。

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
    case "failed", "error":
        // v0.14.1: managed-model 协议是 "failed", Anthropic 协议是 "error",均路由到此
        // 推荐改用 ChatStreamWithUsage,errCh 已自动归并
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

**上游默认**（Gateway v0.13.x 在 `/models` 响应里填充）:

| Provider | supported_formats | preferred_format | 端点后缀 | Betas 注入 |
|----------|------|------|------|------|
| Anthropic | `["anthropic"]` | `anthropic` | `/anthropic` | 是 (10 项) |
| Acosmi | 同 Anthropic (hardcode 回落) | — | `/anthropic` | 是 |
| DashScope (Qwen) | `["anthropic","openai"]` | `anthropic` | **`/anthropic`** | 是 |
| Zhipu (GLM) | `["anthropic","openai"]` | `anthropic` | **`/anthropic`** | 是 |
| DeepSeek | `["anthropic","openai"]` | `anthropic` | **`/anthropic`** | 是 |
| OpenAI | `["openai"]` | `openai` | `/chat` | 否 |
| VolcEngine (豆包) | `["openai"]` | `openai` | `/chat` | 否 |
| Custom | `["openai"]` | `openai` | `/chat` | 否 |

> **⚠️ 破坏性变更 (v0.13.x, Gateway Phase 3 诚实化)**: `Anthropic` provider 从 `["anthropic","openai"]` 收紧到 `["anthropic"]`。原声明 OpenAI 格式属声明-行为漂移: `/chat` 主链路对 Anthropic provider 仅做 Anthropic body 适配, 无 OpenAI→Anthropic 消息转换, 强行声明会误导调用方。若需对接 Anthropic 模型, 必须使用 `ChatMessages` / `ChatMessagesStream` (走 `/anthropic` 端点)。
>
> **ℹ️ 旧行为提示 (v0.10.0 切换记录)**: DashScope / Zhipu / DeepSeek 在 v0.10.0 起从 `/chat` 默认切到 Anthropic 协议端点 (这三家 Gateway 侧本就内置 Anthropic 兼容端点)。若需保留 `/chat` 路径, 在 Gateway 侧配置 `preferred_format: "openai"`。

> 注: OpenAIAdapter 不注入 Anthropic betas。OpenAI 路由会把 `Effort` / `Thinking.Level`、`OutputConfig`、`ParallelToolCalls` 映射到 OpenAI 顶层字段；其余特殊字段可通过 `ExtraBody` 显式透传。
>
> **Gateway v0.13.x 字段落地**: `/chat` 主链路现已接入 `reasoning_effort` / `response_format` / `parallel_tool_calls` / `extra_body` 4 个 OpenAI wire-format 字段 (修复 F1 根因: 原 `ChatProxyRequest` 绑定结构缺字段导致静默丢失)。`extra_body` 走严格白名单: `frequency_penalty` / `presence_penalty` / `seed` / `user` / `logit_bias` / `stop` / `top_k` / `n` / `logprobs` / `top_logprobs`, 非白名单 key 会被拒绝并记 `gateway.extra_body.rejected` warn 日志。
>
> **ExtraBody 扁平化 (A-01 审计修复)**: SDK `ExtraBody` 按 OpenAI Python SDK 约定直接合入请求体顶层 (`body["seed"]=42` 而非 `body["extra_body"]["seed"]=42`)。Gateway 在 handler 层用 `PromoteOpenAIExtraFromFlat` 从原始 JSON 提取白名单顶层键再合入 `req.ExtraBody`, 全程**无需调用方改写**。双保险: 若你显式使用嵌套 `extra_body: {...}`, Gateway 也会保留该嵌套值优先 (显式 > 隐式)。

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

#### OpenAI 兼容字段映射 (v0.13.0)

当模型被路由到 OpenAI 兼容格式时，SDK 会优先按 OpenAI wire format 直接翻译关键字段：

| SDK 字段 | OpenAI 顶层字段 | 说明 |
|------|------|------|
| `Effort.Level` / `Thinking.Level` | `reasoning_effort` | `max` 会降级为 `high`；`Effort` 优先级高于 `Thinking.Level` |
| `OutputConfig` | `response_format` | `json_schema` 会生成 `{type:"json_schema", json_schema:{schema,strict:true}}` |
| `ParallelToolCalls` | `parallel_tool_calls` | OpenAI 专属；Anthropic 路由忽略 |
| `ExtraBody` | 同名字段直接透传 | 在最后合入，若与 SDK 已生成字段同名，会覆盖 SDK 值 |

这意味着 v0.13.0 起，OpenAI 路由默认不再发送裸 `thinking` / `effort` / `output_config` 字段；若确实需要保留旧透传语义，请显式使用 `ExtraBody`。

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

#### 同 model_id 多 wireFormat 共存 (Gateway 2026-04-26+)

DashScope / Zhipu / DeepSeek 等同时支持 Anthropic / OpenAI 兼容端点的 provider, 支持
**同一个 `modelId` 挂两份不同 `compat_profile` 的托管模型记录**:

```
qwen3.6-plus  +  aliyun_dashscope_anthropic_v1   →  /anthropic 端点
qwen3.6-plus  +  dashscope_openai_compat_v1      →  /chat 端点
```

DB 唯一键升级: `(tenant_id, model_id, compat_profile)` partial unique。
`ListModels()` 缓存里同 `ModelID` 出现两条记录, 各自 `PreferredFormat` 不同:

```go
models, _ := client.ListModels(ctx)
for _, m := range models {
    fmt.Printf("%s [profile=%s] preferred=%s supported=%v\n",
        m.ModelID, m.Provider, m.PreferredFormat, m.SupportedFormats)
}
// 可能输出:
//   qwen3.6-plus [profile=dashscope] preferred=anthropic supported=[anthropic openai]
//   qwen3.6-plus [profile=dashscope] preferred=openai    supported=[openai]
```

**SDK 路由语义**: SDK 端按 `getCachedModel(modelID)` 命中**首条**记录用于 `PreferredFormat`
判定; **endpoint 路径已隐含 wireFormat** (`/anthropic` vs `/chat`), 后端按
endpoint 类型选**正确的那条** ManagedModel —— SDK 调用方无需感知双记录, 透明工作。

> 业务场景: 一个 model_id 想同时服务 Anthropic / OpenAI 两类客户 (例如
> Claude 客户端走 Anthropic, ChatGPT 客户端走 OpenAI), 配两份各自独立的 API key /
> endpoint / capabilities。

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

#### 模型白名单自动同步 (Gateway 2026-04-26+)

历史问题: tk-dist `entitlements.allowed_models` 字段是套餐购买时写入的字符串数组快照,
管理员在 Gateway 加新 ManagedModel 时**不会自动更新存量用户白名单** → 用户调用新模型
返回 403 "权益包不包含此模型"。

**Gateway 侧已加入三层闭环**:

1. **启动追平**: nexus-backend 启动时跑一次 `SyncAllManagedModelWhitelist`, 把所有
   `is_enabled=true` 的 `managed_models.model_id` 合并进所有 ACTIVE TOKEN_PACKAGE
   `entitlements.allowed_models`。
2. **Create/Update 增量同步**: 管理员后台新建或启用一个 ManagedModel, 后端 hook
   异步同步该 model_id 到所有付费 entitlement 白名单。
3. **Hold 失败兜底**: 如果用户首次调用碰上 `IsModelNotAllowed` 且该 model_id 是
   `ACTIVE` 状态, 后端**自动同步白名单 + 重试一次 Hold**。SDK 调用方**感知不到**这次内部重试。

**SDK 端无需任何改动**, 只需对 403 响应做正常兜底处理:

```go
resp, err := client.Chat(ctx, modelID, req)
if err != nil {
    var apiErr *acosmi.APIError
    if errors.As(err, &apiErr) && apiErr.StatusCode == 403 {
        // 极端情况下兜底失败 (跨库连接故障 / 模型已禁用), 文案会提示
        // "已尝试自动同步白名单仍失败, 请联系管理员"
        log.Printf("model not in plan: %s", apiErr.Message)
    }
}
```

> **关于 `MONTHLY_QUOTA` / `REGISTRATION_BONUS` / `INVITE_REWARD` 类型**:
> 这些 entitlement 的 `allowed_models` 设计上为空 (按 type 维度授权, 不限模型),
> 同步只覆盖 `TOKEN_PACKAGE` 类型 (套餐购买快照需追平)。

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
    ParallelToolCalls *bool             // v0.13.0: OpenAI 顶层 parallel_tool_calls
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

### 请求前防御与 Ephemeral 历史剥离

如果你的上层会回放多轮历史，推荐同时启用底线防御和自动剥离 ephemeral 历史：

```go
import "github.com/acosmi/acosmi-sdk-go/sanitize"

client.SetDefensiveSanitize(sanitize.MinimalSanitizeConfig{
    MaxImageBytes:    5 * 1024 * 1024,
    MaxVideoBytes:    50 * 1024 * 1024,
    MaxPDFBytes:      10 * 1024 * 1024,
    MaxMessagesTurns: 100,
})
client.SetAutoStripEphemeralHistory(true)
```

关键行为：

- `SetDefensiveSanitize(...)` 会在 `Chat` / `ChatStream` / `ChatMessages*` 发送前执行底线检查，提前拦截超大 base64、多轮历史过深、永久禁止 block 等问题。
- `SetAutoStripEphemeralHistory(true)` 会在历史回放前自动剥离被网关标记为 `acosmi_ephemeral:true` 的 block，典型包括 `thinking`、`redacted_thinking`、部分 server tool block。
- 该标记是 in-band 协议：网关直接在 `content_block_start.content_block` 里注入 `acosmi_ephemeral:true`，SDK 不需要维护额外状态。
- 零值兼容旧行为：未开启时不会改变现有请求路径或序列化结果。

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

> 本节只保留 SDK 使用者最需要关心的兼容点与破坏性变更。更细的网关实现背景、审计过程和分阶段交付记录，建议查主仓架构文档。

### v0.14.1 (2026-04-26) — 错误码细化 V2 P0

- **`event: error` 路由** (`ChatStreamWithUsage`): 同时识别 `failed` (acosmi 协议) 与 `error` (Anthropic 协议),均路由到 `errCh`。≤v0.14.0 在 `/managed-models/<id>/anthropic` 路径上拿不到结构化错误,建议升级。
- **`parseStreamError` 三态 schema**: `error` 字段用 `json.RawMessage` 接收,运行时区分 string (acosmi 老协议) / object (Anthropic) / 缺失。Anthropic 纯净格式下 `Code` 自动从 `error.type` 兜底,避免 `errors.As` 拿到空 Code 无法决策。
- **`*StreamError` 字段稳定**: `Code` / `Retryable` / `Message` / `RawError` / `Stage` 五字段无破坏性变更, `Error()` 文案严格保留。
- **新错误码** (与网关 `gwerrors.Kind` 对齐): `upstream_timeout` / `upstream_disconnect` / `upstream_unreachable` / `upstream_malformed` / `client_canceled`,详见 §4.3 错误码表。
- **网关侧关联** (无破坏): 透明重试 200ms 单次退避吃掉 80%+ 瞬断, 5 family provider 计费按 token 不双扣; admin 看板 `WHERE status` 改用 `NOT IN ('success','pending_settle')` 反向兜底,新 kind 自动入"错误"统计。

### v0.14.0 (2026-04-26)

- **冷缓存根治**: `Chat` / `ChatMessages` / `ChatStream` / `ChatMessagesStream` 在
  模型缓存未命中时**自动触发一次 `ListModels()` 刷新**, 仍未找到返回
  `*ModelNotFoundError`; 不再静默回退到 Anthropic 路由 (修复 F2 根因)
  - 调用方可 `errors.As(err, &mnf)` 捕获处理。
- **Adapter 路由注释澄清**: `adapter.go` 注释明确声明优先级链
  `PreferredFormat → SupportedFormats → provider 名硬编码 fallback`,
  与 `getAdapterForModel` 实际行为对齐。
- **网关侧能力对齐 (docs)**:
  - § 4.3 § 同 model_id 多 wireFormat 共存 — DashScope/Zhipu/DeepSeek 现可挂同
    model_id 的 anthropic + openai 双 ManagedModel 记录, DB 唯一键升级到
    `(tenant_id, model_id, compat_profile)`; SDK 调用透明无需感知, 后端按 endpoint
    路径自动选对应记录。
  - § 4.4 § 模型白名单自动同步 — 三层闭环 (启动追平 + Create/Update 增量 + Hold
    失败兜底); SDK 端 403 兜底处理建议; 类型语义 (仅 TOKEN_PACKAGE 同步,
    其他 type allowed_models 设计上为空 = 不限模型)。
  - 错误文案诚实化: 老版"权益包不包含此模型,请购买"误导付费用户去重新购买,
    新版告知"已尝试自动同步,联系管理员"。

### v0.13.2 / v0.13.1 (2026-04-22)

- SDK 公共 API 无新增；主要是文档和关联网关能力对齐。
- Anthropic 路由的上游端点统一以 capability preset 为单一来源，避免 `/v1/messages` 漂移。
- Zhipu 补齐 Anthropic preset 后，可与 DashScope / DeepSeek 一样稳定走 `/anthropic` 管线。
- `SetAutoStripEphemeralHistory(true)` 所依赖的 in-band `acosmi_ephemeral` 标记链路已完整可用。

### v0.13.0 (2026-04-22)

- OpenAI 路由补齐 3 个关键字段映射：`reasoning_effort`、`response_format`、`parallel_tool_calls`。
- `ChatRequest` 新增 `ParallelToolCalls *bool`。
- 轻微兼容变更：OpenAI 路由不再默认发送裸 `thinking` / `effort` / `output_config`，如需旧透传语义请改用 `ExtraBody`。
- 相关说明见 §4.3 “OpenAI 兼容字段映射” 与 §6 `ChatRequest`。

### v0.11.0 (2026-04-22)

- 新增 `sanitize` 子包。
- `Client` 新增 `SetDefensiveSanitize(...)` 与 `SetAutoStripEphemeralHistory(bool)` 两个钩子。
- `StreamEvent` 新增 block 元数据：`BlockIndex` / `BlockType` / `Ephemeral`。
- 相关说明见 §8 “请求前防御与 Ephemeral 历史剥离”。

### v0.10.0 (2026-04-22) ⚠️ 破坏性

- Adapter 选择从“硬编码 provider”切换为“优先读取 `PreferredFormat` / `SupportedFormats`”。
- DashScope / Zhipu / DeepSeek 在上游声明 `preferred_format: "anthropic"` 时，会从 `/chat` 切到 `/anthropic`。
- 旧 Gateway 未返回这两个字段时，SDK 仍按历史 provider 规则回退，保持向后兼容。
- 相关说明见 §4.3 “Anthropic 原生格式 — ChatMessages (V8)”。

### v0.9.0 / v0.8.0

- 新增三档 `Thinking Level` API：`off` / `high` / `max`。
- `Level` 非空时，SDK 自动组装 `thinking` + `effort` + `max_tokens`；`Level=""` 时保持 v0.8.0 passthrough 兼容语义。
- 相关说明见 §4.3 “思考级别 (Thinking Level)”。

### v0.6.0 (2026-04-13)

- 新增通知管理、设备注册、通知偏好相关 API 和类型。
- 相关说明见 §4.8 ~ §4.11 与 §6 “通知”。

### v0.5.0 (2026-04-11)

- 引入 `ProviderAdapter`，形成 Anthropic / OpenAI 两条主路由。
- `Chat` / `ChatStream` / `ChatMessages` / `ChatMessagesStream` 开始按 provider 自动切换端点和格式。
- OpenAI SSE → Anthropic 事件转换、自定义响应转换也在这一版引入。

### v0.4.1 / v0.4.0 (2026-04-10)

- 引入 Anthropic 原生接口：`ChatMessages()` / `ChatMessagesStream()`。
- `ChatResponse` 统一成 Anthropic content block 形态，消费方不再需要区分 OpenAI / Anthropic 响应结构。
- 错误解析、betas 传递、Anthropic usage/stop sequence 等基础契约在这一阶段补齐。

### v0.3.x / v0.2.x / v0.1.0

- `v0.3.x`：模型能力矩阵、搜索来源、`ChatStreamWithUsage` 四通道返回、开发手册补全。
- `v0.2.x`：余额 Header、结算事件、扩展字段、模型缓存、`LoginWithHandler`。
- `v0.1.0`：初始发布，合并 desktop-sdk-go 与 jineng-sdk-go。

---

MIT License | Copyright (c) 2026 Acosmi
