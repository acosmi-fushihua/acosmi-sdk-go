# Acosmi Go SDK 开发手册

> v0.19.0 | Go 1.22+ | MIT

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
  - [4.12 CrabCode bug 报告](#412-crabcode-bug-报告-v30-v0170)
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
    ServerURL:   "",                       // 零值 → https://acosmi.com (默认); 国际站传 https://acosmi.ai
    Store:       nil,                      // 默认 ~/.acosmi/tokens.json
    HTTPClient:  nil,                      // 默认无全局超时 (避免截断 SSE 流)
    RetryPolicy: acosmi.DefaultRetryPolicy, // v0.15+: 重试策略, nil = 禁用 (老行为)
})
```

> `HTTPClient` 不设全局 `Timeout` 是有意为之 — 全局超时会截断流式聊天。通过 `context.Context` 控制超时。

> **v0.15+ RetryPolicy** (opt-in, 0 破坏性): 启用后 GET 类查询自动 2x retry, **POST chat/messages 仍 0 retry** (计费安全). 详见 [§ 12 v0.15 段](#12-版本记录).

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

#### 并发授权语义 (v0.15.1+)

`ensureToken` 是所有 API 方法 (含 WebSocket / SSE / 商城 / 钱包) 的内部 token 闸门。v0.15.1 起按**三态**语义工作，启动期 fan-out 调用不再误报：

| 状态 | 行为 | 错误信息 |
|---|---|---|
| Token 已就绪 | 立即放行 | — |
| Token=nil + Login 进行中 | **阻塞等待**直至 token 就绪或 ctx 超时 | (成功或 `waiting for token: <ctx err>`) |
| Token=nil + Login 未启动 | **fail-fast** (保留旧行为) | `not authorized, call Login() first` |

**适用场景**:

```go
client, _ := acosmi.NewClient(acosmi.Config{...})

// 推荐用法: Login 与 API 调用可并发触发, 无需手动同步
go client.Login(ctx, "MyApp", acosmi.AllScopes())  // 异步开浏览器

// 以下并发调用全部安全 — Login 完成后统一放行, 0 条 "not authorized" 误报
go client.ListModels(ctx)        // 等待 → 拿到 token → 200
go client.GetBalance(ctx)        // 等待 → 拿到 token → 200
go client.WSConnect(ctx)         // 等待 → 拿到 token → 升级握手
```

**红线**:
1. 调用方未触发 Login 即调 API → 立即返 `call Login() first` (与 v0.15.0 行为一致, 错误信息保留)
2. Login 进行中但 ctx 先超时 → 返 `waiting for token: context deadline exceeded` (`errors.Is(err, context.DeadlineExceeded)` 链兼容)
3. 公共端点 (`doPublicJSON`) 仍以"匿名兜底"调用 ensureToken 并忽略错误 — 未授权时走匿名路径，行为不变
4. Logout 后 fail-fast 立即生效；下一次 Login 重新触发等待→唤醒流程

> **修复背景**: v0.15.0 及之前 `ensureToken` 仅有"nil → 立即报错 / 有效 → 返回"二态机, 启动期 4 个并发 fan-out 调用 (ws / ListModels / GetBalance / harness handshake) 各自报 `not authorized` WARN 而非协同等待 Login 完成。v0.15.1 加入 `tokenReady` channel + `loginInFlight` 标志解决该问题；已授权场景零额外开销 (channel 已 close)。

### 4.3 AI 模型服务

> scope: `ai`

#### 模型列表

```go
// 推荐: 拿 entitlement 过滤状态用于 UI 降级提示 (v0.18.1+)
models, status, err := client.ListModelsWithStatus(ctx)
if err != nil { /* ... */ }
for _, m := range models {
    fmt.Printf("%s (%s/%s) 上下文:%d 输出:%d\n",
        m.Name, m.Provider, m.ModelID, m.ContextWindow, m.MaxTokens)
    if m.BucketInfo != nil {
        // V0.18+: 用户已购模型自带余量信息. 推荐用 IsCommercial() 大小写不敏感判定
        kind := "generic"
        if m.BucketInfo.IsCommercial() { kind = "commercial" }
        fmt.Printf("  剩余 %d ETU (共 %d), %s 桶, 共享池 %d ETU\n",
            m.BucketInfo.RemainingEtu, m.BucketInfo.QuotaEtu, kind, m.BucketInfo.SharedPoolEtu)
    }
}
// 据 status 决定 UI 降级提示
switch status {
case acosmi.FilterStatusOK: // 正常按 entitlement 过滤, 余量字段可用
case acosmi.FilterStatusFallbackTkdistError, acosmi.FilterStatusFallbackTkdistSkew:
    // tk-dist 上游不可达或部署版本错位, UI toast: "服务降级, 模型列表临时显示全部"
case acosmi.FilterStatusDisabledByFlag:
    // 运维灰度回滚中, 不显示 BucketInfo
}

// 兼容: 不关心 status 的旧调用方式仍可用 (向后兼容, 不会废弃)
models, _ = client.ListModels(ctx)
```

> **V0.18+ Entitlement 过滤行为** (V30 → V30 二轮 2026-04-29):
>
> `ListModels` 返回的列表**只包含调用方当前用户已购套餐内的模型**, 而非平台全量。商品创建时勾选的模型集合直接透传到 SDK, 客户端无需自己过滤。
>
> **V30 二轮架构变更 (v0.18.1)**: 端点超载分离, admin 后台必须改调 `/api/v4/managed-models/admin` 拿完整视图; SDK 用户调 `ListModels` (即 `mm.GET ""`) 即便是 admin role 也会**走 entitlement 过滤** — 跨权拿 platform 全量+敏感字段 (endpoint/providerConfig/pricing) 已在 V30 二轮根治.
>
> | 调用方 | 端点 | 行为 | BucketInfo |
> |---|---|---|---|
> | 普通用户 (web JWT) | `mm.GET ""` (即 SDK ListModels) | 返**用户已购模型并集** | 非 nil, 含 quota/used/remaining/bucketClass/expiresAt |
> | desktop OAuth `models` scope | `mm.GET ""` | 同普通用户 (admin role 也过滤) | 非 nil |
> | 内部测试 (X-Internal-Bypass header + 配置密钥) | `mm.GET ""` | 返全量 | nil |
> | tk-dist 不可达 (fail-OPEN 兜底) | `mm.GET ""` | 返全量 | nil |
> | V9 老用户无桶记录 (灰度兜底) | `mm.GET ""` | 返全量 | nil |
> | Web admin 后台 (浏览器登录) | `/admin` (新端点, SDK 不调用) | 返完整 ManagedModelResponse + 含 disabled | n/a |
>
> **响应头 `X-Entitlement-Filter-Status`** (通过 `ListModelsWithStatus` 返回的 `FilterStatus` 暴露). 取值与 `acosmi.FilterStatus*` 常量一一对应:
> - `FilterStatusOK` (`ok`): 正常按用户 entitlement 过滤
> - `FilterStatusAdminBypass` (`admin-bypass`): SDK 用户**永不会拿到** (V30 二轮后只有 ListAdmin 端点会, SDK 不调用)
> - `FilterStatusInternalBypass` (`internal-bypass`): X-Internal-Bypass header 命中 (CI/bot)
> - `FilterStatusDisabledByFlag` (`disabled-by-flag`): 运维 ENV 灰度回滚中
> - `FilterStatusFallbackTkdistError` (`fallback-tkdist-error`): tk-dist RPC 失败 fail-OPEN, UI toast 提示
> - `FilterStatusFallbackTkdistSkew` (`fallback-tkdist-deployment-skew`): tk-dist 返 404, Java 部署版本未升级 V30, **SRE 信号**
> - `FilterStatusFallbackNoBuckets` (`fallback-no-buckets`): V9 老用户 fallback
> - `FilterStatusFallbackMissingUser` (`fallback-missing-userid`): 防御性, 不应出现
> - `FilterStatusUnknown` (`""`): 未知/缺失 (调用了老 nexus, header 不存在 — 按 v0.17 行为兜底)
>
> **多桶聚合规则**:
> - `QuotaEtu/UsedEtu/RemainingEtu`: 该 modelId 下用户全部 active 桶求和
> - `SharedPoolEtu` (v0.18+): 求和量中"来自通配桶 (model_id='*')"的部分; 该值会被其他模型联动消耗 — UI 推荐分两栏显示"自有 + 共享池"
> - `BucketClass/ExpiresAt`: 取最高优先级桶 (alive > expired > commercial > exact > expiresAt 早)
> - `Expired`: 全部桶都过期才置 true
>
> **典型场景**:
> - 平台 5 模型 a/b/c/d/e, 用户买了 a/b 套餐 → ListModels 返 2 个 (a/b), 选 c/d/e 时上层 UI 不显示
> - 用户买套餐 1 (a/b 1000ETU) + 套餐 2 (e/f 500ETU) → ListModels 返 4 个, 各自带对应桶余额
> - 用户对模型 a 有精确桶 1000 + 通配桶 500 (allowedModels=[a,b,c]) → ListModels 返 a 的 BucketInfo `QuotaEtu=1500, SharedPoolEtu=500`, 用户跑 b 也会让 a 的"剩余"下降 (这是设计, UI 应明示)

> ⚠️ **strict-mode 反序列化客户端注意**: v0.18 在 `ManagedModel` 新增 `BucketInfo *BucketInfo` 字段。若调用方用 `json.NewDecoder + DisallowUnknownFields()` 严格解码,**老 v0.17 schema 客户端在升级到 v0.18 nexus 后会拒绝解析含 bucketInfo 的响应**。lenient (默认 `json.Unmarshal`) 客户端不受影响。

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

**网关侧 fallback (2026-04-28)**: 即使下游 provider 不支持原生 server tool (DeepSeek / Zhipu / DashScope / VolcEngine 的 Anthropic-format 路径, `caps.SupportsWebSearch=false`), SDK 仍可正常发送 `ServerTools: [WebSearchTool]`。网关自动:

1. 检测请求 body 中的 server tool, 改写为 client function tool 让上游模型以 function-calling 形式触发
2. 模型 emit `tool_use(name="web_search")` 后, 网关同步调本地配置的搜索插件 (阿里云 dashscope-web-search 优先 / 博查 bocha-web-search 兜底; 智普暂未启用)
3. 用 `tool_result` 续轮上游, 拿到模型基于真实搜索结果整合后的最终答案
4. 合并两轮 SSE 流给 SDK: 单条 message 内含 `text + tool_use + final_text`, content_block index 单调递增

**前置条件**: 管理员在 admin 控制台创建该托管模型时勾选阿里云 / 博查搜索插件 (写入 `managed_model.DefaultToolIDs`)。未勾选则 fallback 报错 `no usable plugin (dashscope/bocha) in DefaultToolIDs`, SDK 收到 `event:error`。

**计费**: 双轮上游均计费 (input/output token 求和后通过单次 RecordUsage 写入); 搜索插件按其 `PricingType` 单独计费。

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
if caps.SupportsMaxEffort {
    req.Thinking = acosmi.NewThinkingConfig(acosmi.ThinkingMax)
}
```

#### DeepSeek-anthropic 接入 (Gateway 2026-04-27+)

DeepSeek 在标准 Anthropic 兼容端点 (`/anthropic/v1/messages`) 上扩展了三个**私有字段**控制思考 / JSON 输出, 这些字段不属于 Anthropic-official 协议:

| DeepSeek 字段 | 形态 | 用途 |
|---|---|---|
| `thinking` | `{"type":"enabled"\|"disabled"}` | 思考开关 |
| `output_config` | `{"effort":"high"\|"max"}` | 思考强度 (low/medium → high; xhigh → max) |
| `response_format` | `{"type":"json_object"}` | JSON Output |

**网关闸门**: 仅 `deepseek_anthropic_v1` profile 的 capability preset 声明 `SupportsOutputConfig=true` + `SupportsResponseFormat=true`; 其他 Anthropic-wire provider (Anthropic-official / DashScope-anthropic / Zhipu-anthropic / OpenRouter / third-party) 由 sanitizer 自动剥除这些字段, 防 400。

##### 🆕 网关 wire normalize (Gateway 2026-04-28+)

复盘: SDK 按 Anthropic 标准 wire 发出 `thinking:{type:"adaptive"}` + 顶层 `effort:{level:"high"}`, DeepSeek 兼容层 schema 仅识别 `enabled/disabled` 且 effort 在 `output_config` 内, 表层报 400 `"content[].thinking ... must be passed back"` (借用 Anthropic 文案的兜底错误, 实际触发条件是 schema 字段未识别)。stream:true 路径校验宽松放过, stream:false 路径严格校验 → 同 body 在两路径行为不一致。

acosmi 网关 sanitizer 已加 step 4.5 (`normalizeThinkingAndEffort`), 由 `deepseek_anthropic_v1` preset 的 `ThinkingTypeAlias / EffortHandling=ToOutputConfig / EffortLevelAlias` 三字段驱动自动翻译:

| SDK 发出 (Anthropic 标准) | 网关翻译为 (DeepSeek 方言) |
|---|---|
| `thinking:{type:"adaptive"}` | `thinking:{type:"enabled"}` |
| 顶层 `effort:{level:"high"}` | `output_config:{effort:"high"}` |
| 顶层 `effort:{level:"low" \| "medium"}` | `output_config:{effort:"high"}` (DeepSeek 内部映射) |
| 顶层 `effort:{level:"xhigh"}` | `output_config:{effort:"max"}` |
| `thinking:{type:"disabled"}` | 原样透传 |

**意义**: 走 acosmi 网关 (`acosmi.com` / `acosmi.ai`) 的所有调用方, **可以直接用 SDK 高级 API** (`Thinking: NewThinkingConfig(ThinkingMax)` / `Thinking.Level=ThinkingHigh`), 无需关心 DeepSeek 方言。下文 ⚠️ 段描述的语义局限**仅在直连 DeepSeek 不经 acosmi 网关时存在** (例如自建代理 / 测试脚本绕开 acosmi.com 直连 `api.deepseek.com/anthropic/v1/messages`)。

> SDK 与第三方 provider wire 直接对话**未被官方支持**: 所有 wire 方言翻译都在 acosmi 网关 sanitizer 完成。需要直连请用 compat 模式手填 (见下), 或改用 provider 官方 SDK。

##### ⚠️ 直连 DeepSeek (绕过 acosmi 网关) 的语义局限

| SDK 入参 | AnthropicAdapter 实际写入 body | DeepSeek 期望 | 结果 |
|---|---|---|---|
| `Thinking.Level=ThinkingMax` | `thinking:{type:"adaptive"}` + 顶层 `effort:{level:"max"}` | `thinking:{type:"enabled"}` + `output_config:{effort:"max"}` | ❌ DeepSeek 不识别顶层 `effort` 键, 深度档位静默退化 |
| `OutputConfig{Format:"json_object"}` | `output_config:{format:"json_object"}` | `response_format:{type:"json_object"}` | ❌ 同名键 (`output_config`) 但内嵌 schema 不同, JSON Output 失效 |
| `Thinking.Level=ThinkingOff` | `thinking:{type:"disabled"}` | 同左 | ✅ 直通 |

> SDK 高级 API 是为 Claude 原生模型设计的; DeepSeek-anthropic 是 v0.13.x 的覆盖盲区。**走 acosmi 网关已由 sanitizer step 4.5 自动翻译** (见上 🆕 段), 高级 API 可正常使用; 直连 DeepSeek 仍需用下面的 compat 模式手填。

##### ✅ 推荐接入: compat 模式 + 原始字段直发

下游 (例如 CrabCode 的"关闭/标注/深度"思考开关) 直接构造 DeepSeek 期望的字段形态:

```go
// 用户在 UI 选择思考档位
var thinkingType, effort string
switch userChoice {
case "关闭":
    thinkingType, effort = "disabled", ""
case "标注":
    thinkingType, effort = "enabled", "high"
case "深度":
    thinkingType, effort = "enabled", "max"
}

// MaxTokens 按档位选: 关闭=8192 / 标注=30000 / 深度=100000 (含思考 + answer)
maxTokens := 8192
switch userChoice {
case "标注":
    maxTokens = 30000
case "深度":
    maxTokens = 100000
}

req := &acosmi.ChatRequest{
    Messages:  msgs,
    MaxTokens: maxTokens,
    // 不用 Thinking.Level (高级 API), 直接 compat 模式手填字段
    Thinking: &acosmi.ThinkingConfig{Type: thinkingType},
    ExtraBody: map[string]any{
        // DeepSeek 私有字段, AnthropicAdapter 在 body 末尾覆盖任何 typed 字段
        "output_config":   map[string]any{"effort": effort}, // 关闭档可省略
        "response_format": map[string]any{"type": "json_object"}, // 仅需 JSON Output 时
    },
}
```

**关键点**:

- **不要设 `req.Thinking.Level`**: 一旦设了, SDK 会接管 `thinking` / `effort` / `max_tokens` 三键, 在 DeepSeek 上语义错位。
- **不要设 `req.OutputConfig`**: SDK 会写 `output_config:{format,schema}` 形态, 与 DeepSeek 期望的 `{effort:...}` 键冲突 (同名异构)。
- **`ExtraBody` 在 adapter 末尾覆盖**: 即使你同时设了 `OutputConfig`, ExtraBody 里的 `output_config` 仍会胜出 (`adapter_anthropic.go:102-104`)。
- **ResponseFormat 通道 (Gateway 2026-04-27+)**: 后端 `AnthropicProxyRequest` 已加 `response_format` 字段绑定 + 专属 `adaptAnthropicDeepSeek` 适配器写入 body, 不再被 Gin 静默丢弃。

##### 思考开关三档完整示例

```go
// 关闭思考: 仅传 thinking
req := &acosmi.ChatRequest{
    Messages:  msgs,
    MaxTokens: 8192, // 8K — 关闭思考时全额给 answer, 留足空间防代码/长答截断
    Thinking:  &acosmi.ThinkingConfig{Type: "disabled"},
}

// 标注思考: thinking=enabled + effort=high
req := &acosmi.ChatRequest{
    Messages:  msgs,
    MaxTokens: 30000, // 30K — 标准思考档位常用值
    Thinking:  &acosmi.ThinkingConfig{Type: "enabled"},
    ExtraBody: map[string]any{
        "output_config": map[string]any{"effort": "high"},
    },
}

// 深度思考 + JSON Output: 三字段全开
req := &acosmi.ChatRequest{
    Messages:  msgs,
    MaxTokens: 100000, // 100K — 深度思考 + JSON 输出共享额度, 含思考链 + answer
    Thinking:  &acosmi.ThinkingConfig{Type: "enabled"},
    ExtraBody: map[string]any{
        "output_config":   map[string]any{"effort": "max"},
        "response_format": map[string]any{"type": "json_object"},
    },
}
```

> `max_tokens` 是响应总额度 (思考 block + 文本 block + tool_use 全部计入), 不是单独的"思考长度"。DeepSeek 1M context / 300K+ output 上限非常宽松, 上述三档 (8K/30K/100K) 是体验/成本平衡点; 若 schema 复杂或 answer 长, 自行按需上调防截断。
>
> JSON Output 注意 (DeepSeek 文档): system / user prompt 必须含 "json" 字样并给出输出样例; `max_tokens` 要够防截断; 偶发返回空 content (网关已用 `KindEmptyResponse` 兜底重试)。

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

#### V29: Per-Model 桶计费 (v0.16.0+)

**背景**: 传统单池扣减不区分模型,Opus 与 DeepSeek 单价相差 200x 导致"白嫖额度刷高价模型"赔本。
V29 把套餐拆成**按模型的独立桶**,每个桶用 ETU (Equivalent Token Unit) 折算计量。

**核心概念**:
- **ETU**: 桶内部记账单位,= raw_token × coef (input/output/cache_read 三系数加权)
- **COMMERCIAL 桶**: 套餐购买的精确 modelId 桶,真金白银
- **GENERIC 桶**: 注册赠送/邀请奖励/月度免费的通配桶 (model_id='*'),仅允许便宜模型白名单
- **桶选择优先级**: COMMERCIAL > GENERIC,同 class 内精确匹配 > 通配兜底

```go
// 1. 查指定模型剩余 (raw + ETU). 切换模型时建议预查, HasQuota=false 则拦截
m, err := client.GetByModel(ctx, "deepseek-v3")
if err != nil {
    return err
}
if !m.HasQuota {
    fmt.Println("该模型已无额度, 请购买套餐或换模型")
    return nil
}
fmt.Printf("DeepSeek 剩余: %d raw token (%d ETU)\n",
    m.RawTokenRemaining, m.ETURemaining)
if m.PrimaryBucket != nil {
    fmt.Printf("  来源桶: %s class=%s\n",
        m.PrimaryBucket.BucketID, m.PrimaryBucket.BucketClass)
}

// 2. 列出全部桶 (个人中心多桶 hero 数据源)
buckets, _ := client.ListBuckets(ctx)
for _, b := range buckets {
    label := b.ModelID
    if b.ModelID == "*" {
        label = "GENERIC (允许: " + b.AllowedModelsJSON + ")"
    }
    fmt.Printf("  [%s] %s: %d/%d ETU\n", b.BucketClass, label, b.TokenUsed, b.TokenQuota)
}

// 3. 拉系数表 (UI 反向估算 raw token 用); SDK 内置 8s TTL 缓存避免风暴
coefs, _ := client.ListCoefficients(ctx)
for _, c := range coefs {
    fmt.Printf("  %s: in=%.4f out=%.4f cacheR=%.4f v%d\n",
        c.ModelID, c.InputCoef, c.OutputCoef, c.CacheReadCoef, c.Version)
}
// admin 改系数后立即失效缓存
client.InvalidateCoefficientCache()
```

**Chat 响应自动回填模型剩余** (Header `X-Token-Remaining-Model` / `X-Token-Remaining-Model-ETU`):

```go
resp, _ := client.Chat(ctx, "deepseek-v3", req)
// adapter 初始化为 -1 = 服务端未返回该头 (灰度期或单池模式); ≥ 0 = 真实剩余
if resp.ModelTokenRemaining >= 0 {
    fmt.Printf("当前模型剩余: %d raw token (%d ETU)\n",
        resp.ModelTokenRemaining, resp.ModelTokenRemainingETU)
}
```

**典型集成场景**:

| 用例 | 推荐 API | 缓存 |
|------|----------|------|
| 模型选择器显示余量 badge | `GetByModel` | **无 SDK 缓存**, 调用方自行节流 (建议 ≥ 5s/模型) |
| 个人中心多桶展示 | `ListBuckets` | **无 SDK 缓存**, 应用层每次刷新主动调 |
| Chat 响应后实时刷新当前模型 | `resp.ModelTokenRemaining` (响应头自动填充) | 0 额外 RTT |
| Pricing 页显示 ETU 系数表 | `ListCoefficients` | **8s TTL 内置缓存** (`InvalidateCoefficientCache()` 手动失效) |

> **缓存边界提醒**: 仅 `ListCoefficients` 在 SDK 层带 8s TTL (实证: `client.go` `coefCacheTTL = 8 * time.Second`)。
> 其余三个 API 每次都走 HTTP, 调用方需自行控制频率 — 否则 chat 高频场景会拖慢 RTT。

**注意事项**:
- ETU 与 raw token 不是 1:1: 用 `OutputCoef` 反向除可估算 raw 等值,但精确量在 hold/settle 时才确定
- GENERIC 桶模型受 `AllowedModelsJSON` 白名单限制,调白名单外的模型会 403 `MODEL_NOT_ALLOWED`
- 灰度期老用户 entitlement 可能无桶 (`ListBuckets` 返回空), 此时仍走 legacy 单池路径,不影响 Chat 调用

#### V0.19: 钱包总览 — 免费/付费切分 (v0.19.0+)

**痛点**: V29 暴露的是"按 modelId 聚合"或"全部桶 raw 列表", 个人中心钱包栏想一次显示
"我还有 X 万免费 Token + Y 万付费 Token, 各自最近 Z 天到期"必须客户端自己 filter+sum,
易写错且每个调用方重复实现.

**解决**: 新增 `GetQuotaSummary` 端点 + SDK 一次拿账户级钱包视图.

```go
// 个人中心钱包栏一次拿全
sum, err := client.GetQuotaSummary(ctx)
if err != nil {
    return err
}

// 总额 — 仅 alive 桶 (即真实可消费余额, 与 RemainingEtu 含过期不同)
fmt.Printf("免费余额: %d ETU\n", sum.FreeTotalEtu)
fmt.Printf("付费余额: %d ETU\n", sum.PaidTotalEtu)

// 最近到期 — 双字段允许分别提示, 单字段会折叠掉一种维度
if sum.NextFreeExpiresAt != nil {
    days := int(time.Until(*sum.NextFreeExpiresAt).Hours() / 24)
    fmt.Printf("免费余额 %d 天后到期\n", days)
}
if sum.NextPaidExpiresAt != nil {
    days := int(time.Until(*sum.NextPaidExpiresAt).Hours() / 24)
    fmt.Printf("付费余额 %d 天后到期\n", days)
}

// 详细桶列表 — 含过期 (UI 列流水/历史/标"已过期")
for _, b := range sum.FreeBuckets {
    status := "alive"
    if b.Expired { status = "expired" }
    fmt.Printf("  [%s] %s: %d/%d (%s)\n", b.BucketClass, b.BucketID, b.TokenRemaining, b.TokenQuota, status)
}
```

**字段映射**:

| 字段 | 含义 | alive-only |
|------|------|------------|
| `FreeTotalEtu` | GENERIC 桶 (月度免费/邀请/试用) tokenRemaining 求和 | ✅ |
| `PaidTotalEtu` | COMMERCIAL 桶 (购买套餐) tokenRemaining 求和 | ✅ |
| `FreeBuckets[]` | GENERIC 桶详情 (含过期, UI 列流水) | ❌ 含全部 |
| `PaidBuckets[]` | COMMERCIAL 桶详情 (含过期) | ❌ 含全部 |
| `NextFreeExpiresAt` | GENERIC alive 桶最早到期 (永久桶不参与) | ✅ |
| `NextPaidExpiresAt` | COMMERCIAL alive 桶最早到期 | ✅ |

**与 BucketInfo (per-model) 的区别**:

| 维度 | `ManagedModel.BucketInfo` (V0.18) | `QuotaSummary` (V0.19) |
|------|-----------------------------------|------------------------|
| 粒度 | 单 modelId 聚合 | 整账户全局 |
| `RemainingEtu` 语义 | 含过期桶 (历史 UX 兼容) | N/A (拆成 FreeTotalEtu/PaidTotalEtu, 仅 alive) |
| 用例 | 模型选择器卡片 "a 模型还能用多少" | 个人中心 "我整体还有多少钱" |
| 走 entitlement 过滤 | ✅ (V30 二轮: 永远过滤) | ❌ (用户必须看自己钱包全量) |

**ManagedModel.BucketInfo 同步增强 (v0.19+)**: 单模型卡片也加了 `FreeRemainingEtu` /
`PaidRemainingEtu` 字段 (alive-only), 推荐用这两个新字段而非 `RemainingEtu`:

```go
models, _ := client.ListModels(ctx)
for _, m := range models {
    if m.BucketInfo == nil { continue }
    // 新方式 (推荐, alive-only)
    fmt.Printf("%s: 免费 %d + 付费 %d ETU 可用\n",
        m.ModelID, m.BucketInfo.FreeRemainingEtu, m.BucketInfo.PaidRemainingEtu)
    // 老方式 (含过期, 仅向后兼容)
    // fmt.Printf("%s: %d ETU\n", m.ModelID, m.BucketInfo.RemainingEtu)
}
```

**鉴权**: JWT 或 Desktop OAuth (`entitlements` scope) — 与 ListBuckets 同 group.

**失败语义**: tk-dist 不可达/5xx → 500 + non-nil err; 空桶用户 (V9 老用户/未购未领) → 200 + 全 0 + 空切片 (`FreeBuckets/PaidBuckets` 永不为 nil, 前端不需 null 检查).

**缓存提醒**: SDK 不内置缓存 — 调用方自行节流, 建议 ≥ 30s/account (个人中心刷新够用).

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

### 4.12 CrabCode bug 报告 (V30, v0.17.0+)

> scope: `account` (任意已登录 token 即可, 不需新增 scope)
> 端点: `POST /api/v4/crabcode_cli_feedback` + `GET /api/v4/crabcode/bug/:id`

CrabCode CLI `/bug` `/feedback` 命令落库通道. 设计目标是让"用户报 bug"独立成环 — 客户端把整段 reportData
(含 description / 环境 / transcript / errors) 提交后, 后端做密钥脱敏并写 PG JSONB, 返回 `feedback_id` +
公开 `detail_url`. 维护者把 `detail_url` 附在 GitHub Issue body 里, 跨租户/无登录可读.

```go
// 1. 上报 bug — reportData 任意 JSON 可编码对象, 后端只解析为 map 做脱敏 + 字段抽取,
//    不做严格 schema 校验 (字段会随客户端版本变).
report := map[string]any{
    "description":   "调 ChatStream 偶发 504",
    "platform":      "darwin",
    "terminal":      "iTerm.app",
    "version":       "v0.1.0",
    "datetime":      time.Now().UTC().Format(time.RFC3339),
    "message_count": 12,
    "transcript":    transcript,    // []map[string]any
    "errors":        []map[string]any{{"error": "EOF", "timestamp": "..."}},
    "lastApiRequest": lastReq,
    "rawTranscriptJsonl": rawJSONL,
}
result, err := client.SubmitBugReport(ctx, report)
fmt.Println(result.FeedbackID)  // <uuid>
fmt.Println(result.DetailURL)   // https://acosmi.com/chat/crabcode/bug/<uuid>

// 2. 取公开 ViewModel (无需 auth — SSR 页面后端 / 维护者 CLI 都用)
view, err := client.GetBugReport(ctx, "<uuid>")
fmt.Println(view.Description, view.Status, len(view.Transcript))
```

**脱敏规则** — 后端服务层在落库前对 reportData 内**所有 string 叶子节点**应用以下正则, 替换为 `[REDACTED:<kind>]`:

| Kind | Pattern | 示例 |
|------|---------|------|
| anthropic-key | `sk-ant-[A-Za-z0-9_\-]{20,}` | `sk-ant-api03-...` |
| openai-key | `sk-(?:proj-)?[A-Za-z0-9_\-]{20,}` | `sk-proj-...` / `sk-...` |
| github-token | `gh[psour]_[A-Za-z0-9]{20,}` | `ghp_...` / `ghs_...` |
| aws-akid | `AKIA[0-9A-Z]{16}` | `AKIAIOSFODNN7EXAMPLE` |
| google-api-key | `AIza[0-9A-Za-z_\-]{35}` | `AIzaSy...` |
| bearer | `(?i)bearer\s+[A-Za-z0-9_\-\.=]{20,}` | `Authorization: Bearer ...` |

调用方**不应**自己做脱敏后再发 — 让服务端兜底是统一规则的来源.

**错误处理**:

```go
result, err := client.SubmitBugReport(ctx, report)
if err != nil {
    var he *acosmi.HTTPError
    if errors.As(err, &he) {
        switch {
        case he.StatusCode == 401:
            // token 过期, 触发 LoginWithHandler 重新拿 token
        case he.StatusCode == 403 && he.Type == "permission_error" &&
             strings.Contains(he.Message, "Custom data retention settings"):
            // 用户所在组织 ZDR — 不能上报, 提示用户复制日志走外部渠道
        case he.StatusCode == 400 && he.Type == "invalid_request_error":
            // reportData 编码失败 (内含 unmarshalable types) → 客户端自查
        case he.StatusCode == 429:
            // 限流 20/h/user, RetryAfter 秒数遵循
        }
    }
}
```

**限流**: POST 端点 `20/h/user` (per-userID), GET 端点 `60/min/IP`. 重试不放过 1 小时窗.

**字段保留约束**: PG JSONB 单行 1GB 上限. 实际 transcript 多 MB 级无压力. 客户端无须自行截断 — 但建议
`rawTranscriptJsonl` 在客户端先截到 `MAX_TRANSCRIPT_READ_BYTES` (CrabCode CLI 已默认这么做).

**未实现 SDK 方法之前** (v0.16.x 及以下), 可以直接 HTTP 直调:

```go
body, _ := json.Marshal(map[string]string{"content": string(mustJSON(reportData))})
req, _ := http.NewRequestWithContext(ctx, "POST",
    "https://acosmi.com/api/v4/crabcode_cli_feedback", bytes.NewReader(body))
req.Header.Set("Authorization", "Bearer "+accessToken)
req.Header.Set("Content-Type", "application/json")
// ... do + parse {feedback_id, detail_url}
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
    SupportedFormats            []string    // v0.10.0: ["anthropic","openai"], 上游可选
    PreferredFormat             string      // v0.10.0: "anthropic" | "openai", 空则取 SupportedFormats[0]
    BucketInfo                  *BucketInfo // v0.18.0: 用户余量聚合, admin / fallback 路径为 nil
}

// V0.18.0 V30 entitlement-listing
type BucketInfo struct {
    QuotaEtu      int64      `json:"quotaEtu"`      // 该 modelId 全部桶配额求和 (单位 ETU)
    UsedEtu       int64      `json:"usedEtu"`
    RemainingEtu  int64      `json:"remainingEtu"`
    SharedPoolEtu int64      `json:"sharedPoolEtu,omitempty"` // 来自通配桶的求和子集, 跨模型可消耗
    ExpiresAt     *time.Time `json:"expiresAt,omitempty"`     // primary 桶到期; nil = 永久
    BucketClass   string     `json:"bucketClass"`             // BucketClassCommercial | BucketClassGeneric
    Expired       bool       `json:"expired,omitempty"`       // 全部桶都过期
}

// V0.18.1 V30 二轮审计 D-P1-3: BucketClass 字面量常量, 推荐用 IsCommercial() 大小写不敏感比较
const (
    BucketClassCommercial = "COMMERCIAL"
    BucketClassGeneric    = "GENERIC"
)
func (b *BucketInfo) IsCommercial() bool // nil-safe; 与上游 EqualFold 对齐, 字面量大小写不敏感

// V0.18.1 V30 二轮审计 D-P1-2: ListModels 响应头 X-Entitlement-Filter-Status 类型化暴露
type FilterStatus string

const (
    FilterStatusOK                  FilterStatus = "ok"
    FilterStatusAdminBypass         FilterStatus = "admin-bypass"                    // SDK 调用永不命中 (V30 二轮端点超载分离后)
    FilterStatusInternalBypass      FilterStatus = "internal-bypass"                 // CI/bot 后门
    FilterStatusDisabledByFlag      FilterStatus = "disabled-by-flag"                // 运维 ENV 灰度回滚
    FilterStatusFallbackTkdistError FilterStatus = "fallback-tkdist-error"           // tk-dist RPC 失败
    FilterStatusFallbackTkdistSkew  FilterStatus = "fallback-tkdist-deployment-skew" // tk-dist 404 部署版本错位
    FilterStatusFallbackNoBuckets   FilterStatus = "fallback-no-buckets"             // V9 老用户
    FilterStatusFallbackMissingUser FilterStatus = "fallback-missing-userid"         // 防御性, 不应出现
    FilterStatusUnknown             FilterStatus = ""                                // 老 nexus / 未知
)

// 推荐: 拿 status 用于 UI 降级提示
func (c *Client) ListModelsWithStatus(ctx context.Context) ([]ManagedModel, FilterStatus, error)
// 兼容: 不关心 status 的旧调用 (向后兼容)
func (c *Client) ListModels(ctx context.Context) ([]ManagedModel, error)

type ModelCapabilities struct {
    // 思考能力
    SupportsThinking, SupportsAdaptiveThinking, SupportsISP       bool
    // 工具与搜索
    SupportsWebSearch, SupportsToolSearch, SupportsStructuredOutput bool
    // 推理控制 (SupportsMaxEffort: 模型支持 thinking_level=max 强度档,
    //          适用于 Opus 4.6 / DeepSeek-V4 等; 详见 §思考级别 段)
    SupportsEffort, SupportsMaxEffort, SupportsFastMode            bool
    SupportsAutoMode                                               bool
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
    // V29 (v0.16.0+): 当前模型剩余 token, 从响应头 X-Token-Remaining-Model[-ETU] 填充
    // 默认初始化为 -1 (与 TokenRemaining 一致); ≥0 = 真实值
    ModelTokenRemaining    int64 // raw token 估算 (UI 展示)
    ModelTokenRemainingETU int64 // 折算后 ETU (调度判定)
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

### V29 Per-Model 桶 (v0.16.0+)

```go
// 单桶视图 — TokenQuota/TokenUsed 单位均是 ETU (折算后), 不是原始 token
type ModelBucket struct {
    BucketID, EntitlementID string
    ModelID                 string  // "*" = 通配 (GENERIC)
    BucketClass             string  // COMMERCIAL | GENERIC
    TokenQuota, TokenUsed, TokenRemaining int64
    CallQuota, CallUsed, CallRemaining    int
    CoefficientVersion                    int
    AllowedModelsJSON                     string // 仅 GENERIC, JSON 数组
}

// GetByModel 响应; PrimaryBucket nil 表示该模型无任何可用桶
type ModelByQuotaResponse struct {
    ModelID                          string
    ETURemaining, RawTokenRemaining  int64
    HasQuota                         bool
    PrimaryBucket                    *ModelBucket
}

// 单条系数 (SDK 8s TTL 缓存)
type ModelCoefficient struct {
    ModelID, TenantID                                 string
    InputCoef, OutputCoef, CacheReadCoef, CacheCreationCoef float64
    Version                                            int
    EffectiveAt                                        string
}
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

### CrabCode bug 报告 (V30, v0.17.0+)

```go
// 提交结果 — POST /api/v4/crabcode_cli_feedback 返回体
type BugReportResult struct {
    FeedbackID string `json:"feedback_id"`  // 服务端生成的 UUID
    DetailURL  string `json:"detail_url"`   // https://<base>/chat/crabcode/bug/<id>
}

// 公开 ViewModel — GET /api/v4/crabcode/bug/:id 返回体
type BugView struct {
    ID             string         `json:"id"`
    Description    string         `json:"description"`               // 已脱敏
    Platform       string         `json:"platform,omitempty"`        // darwin | linux | win32
    Terminal       string         `json:"terminal,omitempty"`        // iTerm.app / Terminal.app
    Version        string         `json:"version,omitempty"`         // CrabCode 版本号
    MessageCount   int            `json:"messageCount"`
    HasErrors      bool           `json:"hasErrors"`
    Status         string         `json:"status"`                    // new | triaging | fixed | wontfix
    ClientDatetime *time.Time     `json:"clientDatetime,omitempty"`  // 客户端 reportData.datetime
    CreatedAt      time.Time      `json:"createdAt"`
    Errors         []any          `json:"errors,omitempty"`          // 已脱敏
    Transcript     []any          `json:"transcript,omitempty"`      // 已脱敏 (前端默认折叠)
    Extras         map[string]any `json:"extras,omitempty"`          // 其它非主字段 (rawTranscriptJsonl / lastApiRequest 等)
}
```

> Errors / Transcript / Extras 用 `[]any` / `map[string]any`: 客户端 reportData schema 会随版本变,
> SDK 不强 typed; 调用方按需做 type assertion.

### 错误

```go
// HTTPError: HTTP 非 2xx 业务错误 (v0.15+, 替代老 fmt.Errorf 字符串错误)
// parseHTTPError 现返回 *HTTPError, 自动解析 Anthropic/OpenAI 双格式 + Retry-After 头
type HTTPError struct {
    StatusCode int    // HTTP 状态码
    Type       string // anthropic.error.type / openai.error.type, 缺失为空
    Message    string // 错误消息
    RetryAfter int    // Retry-After 头解析的秒数, 0 表示未提供
    Body       string // 原始响应体 (截断到 1MB)
}
// Error() 文案与老 fmt.Errorf 完全一致: "HTTP %d: [%s] %s" / "HTTP %d: %s" / "HTTP %d"

// NetworkError: 传输层错误 (v0.15+, 包装 c.http.Do 返回的 timeout/EOF/connection refused 等)
type NetworkError struct {
    Op      string // 操作描述, e.g. "POST /v1/messages"
    URL     string
    Cause   error  // 原始 net 错误
    Timeout bool   // ctx.DeadlineExceeded / net.Error.Timeout()
    EOF     bool   // io.EOF / "unexpected EOF" / "connection reset"
}
// Unwrap() error → 支持 errors.Is 链匹配原始 cause
// IsTimeout() bool / IsEOF() bool — L6 retry policy 用此判定可重试性

type RateLimitError struct { Message, RetryAfter, Raw string }
// 注: RateLimitError 仅 DownloadSkill 匿名下载链路使用 (兼容历史). 其他 429 路径用 *HTTPError + RetryAfter int 字段.

// BusinessError: API 业务层错误 (HTTP 200 但 code != 0, tk-dist 代理透传 yudao 响应)
type BusinessError struct { Code int; Message string }

// OrderTerminalError: 订单到达非成功终态 (FAILED/CANCELLED/CLOSED/EXPIRED/REFUNDED)
// WaitForPayment 在终态非成功时返回
type OrderTerminalError struct { OrderID, Status string }

// 类型断言示例 (v0.15+ 推荐先 HTTPError/NetworkError, 老 RateLimitError/BusinessError 仍兼容)
var he *acosmi.HTTPError
if errors.As(err, &he) {
    if he.StatusCode == 429 && he.RetryAfter > 0 {
        time.Sleep(time.Duration(he.RetryAfter) * time.Second)
    } else if he.StatusCode == 401 {
        // 重新登录...
    }
}
var ne *acosmi.NetworkError
if errors.As(err, &ne) {
    if ne.IsTimeout() { /* 超时, 可考虑重试 */ }
    if ne.IsEOF() { /* 连接断开, 可考虑重试 */ }
}
var rateErr *acosmi.RateLimitError
if errors.As(err, &rateErr) { /* 下载链路限流 (DownloadSkill) */ }
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

### v0.19.0 (2026-04-29) — 钱包总览 + 免费/付费切分 (V30 后续增量)

**背景**: V30 一二轮把 ListModels 接通了 entitlement, 但 SDK 用户实际 UI 渲染时发现痛点:
后端虽分了 COMMERCIAL/GENERIC 桶, SDK 暴露的余量字段是单字段 `RemainingEtu` 含过期 — 个人中心
钱包栏想一次显示"我还有 X 万免费 + Y 万付费 Token, 各自 Z 天到期"必须客户端 filter+sum, 易写错且每个调用方重复实现.

**SDK 端新增 (向后兼容)**:
- 新类型 `QuotaSummary` + `BucketRow` + `client.GetQuotaSummary(ctx)` — 个人中心钱包栏一次拿全, 含 FreeTotalEtu/PaidTotalEtu/FreeBuckets[]/PaidBuckets[]/NextFreeExpiresAt/NextPaidExpiresAt 6 字段 (alive-only 总额 + 含过期详情列表 + 双独立到期字段).
- `BucketInfo` 新增 `FreeRemainingEtu` / `PaidRemainingEtu` 字段 (alive-only, 即真实可消费余额); 旧 `RemainingEtu` 保留但标注"含过期, 历史 UX 兼容, 推荐用 Free/Paid".
- `BucketRow.IsCommercial()` 大小写不敏感判定 (与 `BucketInfo.IsCommercial` 同语义).

**配套 nexus-v4 backend**:
- 新端点 `GET /api/v4/entitlements/quota-summary` (复用 EntitlementHandler.tkdistClient, 走 AuthOrDesktopScope `entitlements` 同 group).
- `aggregateBucketInfo` 注入 alive-only `FreeRemainingEtu` / `PaidRemainingEtu` 到 `BucketInfoResponse`, ListModels 响应每模型卡片现在带 6 字段 (旧 4 + 新 2).

**设计决策 (调研 web/profile + EntitlementServiceImpl + filterModelsByEntitlement 后定):**
1. **命名 = Free/Paid**: 前端 i18n 已用 `'月度免费'/'付费套餐'/'免费试用'`, 用户视角而非 internal `Generic/Commercial`.
2. **expired 桶 = alive-only**: Java 端 `selectBucketForUpdate` 不消费过期桶, 新字段 alive-only 才是真实可消费余额; 旧 `RemainingEtu` 含过期保兼容.
3. **不过滤**: 用户看自己钱包总览必须全量 — `QuotaSummary` 端点不走 entitlement 过滤逻辑.
4. **NextExpiresAt 双字段**: 单字段会折叠"免费 X 天到期 vs 付费 Y 天到期"两条独立 UI 提示能力, 双字段允许分别提示.

**部署 sequence**:
1. 先发 SDK v0.19.0 (向后兼容, 老 v0.18.1 客户端调新/老 nexus 都正常 — 新字段反序列化为 0/nil).
2. 部署 nexus-v4 backend (架构 + 端点补齐) — tk-dist 零修改.
3. 前端按需切到新字段 (老 `RemainingEtu` 仍可用, 不阻塞).

**示例**:
```go
sum, _ := client.GetQuotaSummary(ctx)
fmt.Printf("免费: %d ETU (%v 到期) | 付费: %d ETU (%v 到期)\n",
    sum.FreeTotalEtu, sum.NextFreeExpiresAt,
    sum.PaidTotalEtu, sum.NextPaidExpiresAt)
```

### v0.18.1 (2026-04-29) — V30 二轮审计闭环 (架构根因修复 + 安全加固)

**背景**: V30 一轮审计闭环 22 项后, 二轮代码层深度复核发现端点超载、时序攻击、scope 双写、ctx 不透传、tk-dist 端点无认证 等架构根因问题. 此版本是与 nexus-v4 backend / tk-dist 同步的根因修复, **零破坏**, SDK 用户可直接升级.

**SDK 端新增 (向后兼容)**:
- `BucketClassCommercial` / `BucketClassGeneric` 常量 + `BucketInfo.IsCommercial()` 帮助方法 (大小写不敏感, 与上游 EqualFold 对齐). 推荐 SDK 用户用 `b.IsCommercial()` 而非 `b.BucketClass == "commercial"` 字面量比较.
- `ListModelsWithStatus(ctx) ([]ManagedModel, FilterStatus, error)` — 暴露 `X-Entitlement-Filter-Status` header. UI 可据此判定 `FilterStatusFallbackTkdistError` / `FilterStatusFallbackTkdistSkew` / `FilterStatusDisabledByFlag` 等降级状态, 渲染相应 toast.
- `FilterStatus` 类型 + 9 个常量 (Ok / AdminBypass / InternalBypass / DisabledByFlag / FallbackTkdistError / FallbackTkdistSkew / FallbackNoBuckets / FallbackMissingUser / Unknown).
- 4 新单测 (Expired+Remaining>0 透传 / 非-Z 时区 / SharedPool>Remaining 透传 / IsCommercial 大小写).

**SDK 端不变**:
- `ListModels(ctx) ([]ManagedModel, error)` 行为完全保留 — 新方法不替代旧方法, 老调用方零修改可用.
- 所有 BucketInfo 字段 (QuotaEtu/UsedEtu/RemainingEtu/SharedPoolEtu/ExpiresAt/BucketClass/Expired) wire format 完全不变.

**配套上游修复 (用户感知零变化, 此处仅供 SRE/审计参考)**:
- nexus-v4 backend 端点超载分离: admin 后台改调 `/api/v4/managed-models/admin` 拿完整视图, `mm.GET ""` 收敛为永远 PublicResponse + 永远过滤 (除 X-Internal-Bypass header). `ShouldFilterByEntitlement` 删除 IsAdmin/desktop_client_id 分支.
- nexus-v4 `subtle.ConstantTimeCompare` 防 X-Internal-Bypass 时序攻击.
- nexus-v4 `effectiveBypassSecret` 删除 fallback S2SSecret (零信任, 必须显式 `ENTITLEMENT_FILTER_BYPASS_SECRET` ENV).
- nexus-v4 `BucketRow.ExpiresAt` string → `*time.Time` (锚定 ISO-8601 契约, epoch ms 数字 fail-fast).
- nexus-v4 `ListBucketsTyped(ctx, ...)` 接 ctx, 客户端断连立即终止 RPC + retry, 不再空耗.
- nexus-v4 `InitEntitlementListMetrics` 失败 fail-fast 启动 panic, 不再静默丢失 attack 信号; `/health` 端点暴露 `entitlement_metrics_ready`.
- nexus-v4 fallback 区分: tk-dist 404 (Java 部署版本错位) 走 `fallback-tkdist-deployment-skew` header 让 SRE 立刻识别; metric outcome 仍归 `fallback_tkdist_error` 防基数膨胀.
- tk-dist `/api/entitlements/buckets` 加 S2sAuthInterceptor + permit-all 双侧加固 (V29 遗留漏洞, V30 暴露面放大). 同期顺手堵 `list/by-model/consume-records/coefficients/balance/balance-detail` 6 个 V29 同类裂缝端点.
- tk-dist `EntitlementController.adminList` 加 `@PreAuthorize` 守卫 (V29 任意用户枚举权益漏洞).
- tk-dist HashMap 容量预设防 rehash (重度老用户 1000+ 桶场景).

**部署 sequence**:
1. 先发 SDK v0.18.1 (向后兼容, 零破坏 — 老 v0.18.0 客户端调老/新 nexus 都正常)
2. 部署 tk-dist (Java 端点认证补齐) — 必须先于 nexus, 否则 nexus 调 buckets 会被新 S2sAuthInterceptor 拦
3. 部署 nexus-v4 backend (architecture refactor) — 需重启所有实例
4. 部署 web admin UI (page.tsx 切到 `/api/v4/managed-models/admin`) — 与 backend 同步发避免 admin 看不到 disabled 模型

**紧急回滚**:
- nexus-v4: `ENTITLEMENT_LIST_FILTER_ENABLED=false` 一键回 v0.17 行为 (重启实例生效, **非热回滚**, 文档已修措辞)
- tk-dist: 回滚 application.yaml + DistributionWebMvcConfiguration 即可

### v0.18.0 (2026-04-29) — V30 Entitlement-Listing (ListModels 按用户权益过滤)

> **行为变化, 但向后兼容**: `ListModels` 对非 admin 用户**只返已购模型**而非平台全量, `ManagedModel` 新增 `BucketInfo *BucketInfo` 字段携带余量。strict-mode 反序列化客户端需先升级 SDK 再升级 nexus, lenient 客户端可任意顺序。

- **新行为 — `ListModels` entitlement 过滤** (上游 nexus-v4 V30):
  - **admin/owner/super_admin**: 返 platform 全量, 不附 BucketInfo (后台管理视图)
  - **普通用户 (web JWT / desktop OAuth `ai` scope)**: 返用户**已购套餐覆盖的模型并集**, 每个模型带 `BucketInfo`
  - **fail-OPEN 容错**: tk-dist RPC 失败时返全量但不附 BucketInfo, Chat 阶段 `holdWithAutoSyncRetry` 兜底拒未授权模型
  - **V9 老用户兼容**: 用户无 V29 桶记录时 fallback 返全量 (V30 全量迁移完成后撤掉, 见 backend `~2026-Q3` TODO)
  - **响应头 `X-Entitlement-Filter-Status`**: `ok|admin-bypass|internal-bypass|disabled-by-flag|fallback-tkdist-error|fallback-no-buckets|fallback-missing-userid`, 客户端可据此 UI 提示降级

- **新类型 `BucketInfo`** (types.go):
  ```go
  type BucketInfo struct {
      QuotaEtu      int64      `json:"quotaEtu"`               // 该 modelId 全部桶配额求和 (ETU)
      UsedEtu       int64      `json:"usedEtu"`
      RemainingEtu  int64      `json:"remainingEtu"`
      SharedPoolEtu int64      `json:"sharedPoolEtu,omitempty"` // 来自通配桶的求和子集, 跨模型可消耗
      ExpiresAt     *time.Time `json:"expiresAt,omitempty"`     // primary 桶到期时间, nil = 永久
      BucketClass   string     `json:"bucketClass"`             // COMMERCIAL | GENERIC
      Expired       bool       `json:"expired,omitempty"`       // 全部桶都过期
  }
  ```
  全部字段单位为 **ETU** (Equivalent Token Unit, V29 系数折算后), 不是 raw token. 与 `ListBuckets` / `GetByModel` 单位一致。

- **`ManagedModel` 新增字段**: `BucketInfo *BucketInfo` (omitempty + 指针, admin / fallback 路径为 nil)

- **多桶聚合算法** (上游计算, SDK 直接消费):
  - quota/used/remaining/sharedPool 求和; bucketClass/expiresAt 取最高优先级桶
  - 优先级: alive > expired → commercial > generic → exact > wildcard → expiresAt 早 > 永久
  - 与 backend `selectBucketForUpdate` **近似**对齐 (typePriority 维度未跨进 SDK), 列表层用于展示, **不作消费决策依据** — 实际扣费仍由 Chat 阶段决定

- **典型迁移场景**:
  - 老 v0.17 调用方 `for _, m := range models { ... }` **零修改可用** — 不读 BucketInfo 即视作旧行为
  - 想展示用户余量的客户端: 加 `if m.BucketInfo != nil { ... }` 即可读余量, 用 `Expired` 灰显
  - **strict-mode 反序列化客户端必读**: 升级到 v0.18 SDK 后才能解析 nexus 0.18+ 响应 (新增 bucketInfo 字段会触发 unknown field 错误)

- **不破坏旧客户端**: BucketInfo 是指针 + omitempty, 老 nexus / admin 路径不返此字段, 老 SDK 不读此字段, 任意组合都能工作

- **release sequence (推荐)**:
  1. SDK v0.18.0 先发布 (字段兼容性 SAFE, 单独发不破任何场景)
  2. nexus 灰度: `ENTITLEMENT_LIST_FILTER_ENABLED=false` 部署 → 验证可观测指标 → 切 `=true`
  3. 灰度 1 周观察 `chatacosmi_listmodels_entitlement_filter_total{outcome=*}` 与 `tkdist_buckets_latency` 指标
  4. 通知下游应用可消费 BucketInfo 字段
  5. CrabCode/CrabClaw 等 UI 客户端按需升级到 v0.18 SDK 并加 BucketInfo 渲染

- **配套 backend 紧急回滚**: `ENTITLEMENT_LIST_FILTER_ENABLED=false` 即可恢复 v0.17 行为, 不需要重新部署 SDK

### v0.17.0 (2026-04-29) — V30 CrabCode bug 报告 (📝 文档先于代码)

> 文档先发布的版本; 网关端点已上线生产 (https://acosmi.com), SDK Go 方法 (`SubmitBugReport` / `GetBugReport`)
> 与对应类型 (`BugReportResult` / `BugView`) 在下一个 commit 落地, 标记 v0.17.0. 下游可先按文档 HTTP 直调,
> SDK 落地后切签名零迁移成本.

- **新端点**:
  - `POST /api/v4/crabcode_cli_feedback` — Bearer JWT (复用 `account` scope), 限流 20/h/user
  - `GET /api/v4/crabcode/bug/:bug_id` — 公开 (无 auth), 限流 60/min/IP, Next.js SSR 页面 `https://<base>/chat/crabcode/bug/:id` fetch 此端点渲染
- **新方法**:
  - `Client.SubmitBugReport(ctx, reportData any) (*BugReportResult, error)` — reportData 任意 JSON 可编码对象, 后端只解析为 map 用于脱敏 + 字段抽取
  - `Client.GetBugReport(ctx, bugID string) (*BugView, error)` — 公开读取, 任意 token 即可
- **新类型**: `BugReportResult` / `BugView` (见 §6 "CrabCode bug 报告")
- **服务端脱敏**: 6 类正则 (anthropic-key / openai-key / github-token / aws-akid / google-api-key / bearer)
  在落库前对 reportData 内**所有 string 叶子节点**生效, 调用方无须自行做密钥过滤
- **存储**: PG JSONB 单行 1GB, MB 级 transcript 无压力; 不走 ObjectStorage (接口缺 Download 方法,
  公开页 SSR 拿不到内容; PG 单次 SELECT 取出更直接)
- **detail_url 含 /chat/ 前缀**: Next.js basePath=/chat 是 acosmi web 主路径仓约定. GitHub Issue body
  里出现 `https://acosmi.com/chat/crabcode/bug/<uuid>` 是预期行为
- **scope 不变**: 复用 `account`, **不**新增 `bug` / `feedback` scope (维持 ai/skills/account 三 scope 简化设计)
- **ZDR 钩子**: `BugService.IsZDRUser` 默认返回 false; 启用 ZDR 时由调用方覆盖, 拒绝时返 403 +
  `permission_error` 含关键字 "Custom data retention settings" (客户端按 includes 判断)
- **调用方迁移**: HTTP 直调 → SDK 方法 0 改动 (字段映射一致); 现存 `acosmi_cli_feedback` 类老路径仓里**没有**, 无 301 兼容

### v0.16.0 (2026-04-28)

- **V29 Per-Model 桶计费支持**: 新增 `GetByModel(ctx, modelID)` / `ListBuckets(ctx)` /
  `ListCoefficients(ctx)` / `InvalidateCoefficientCache()` 四个 API。
- 新类型: `ModelBucket` / `ModelByQuotaResponse` / `ModelCoefficient`。
- `ChatResponse` 新增 `ModelTokenRemaining` / `ModelTokenRemainingETU` 两字段,
  从响应头 `X-Token-Remaining-Model[-ETU]` 自动填充, **adapter 初始化为 -1 = 未返回**
  (与原有 `TokenRemaining` 哨兵语义一致, 调用方用 `>= 0` 判定有效值)。
- 缓存边界: **仅 `ListCoefficients` 自带 8s TTL** (`coefCacheTTL` 常量), 其余
  `GetByModel` / `ListBuckets` 每次走 HTTP, 调用方需自行节流。`InvalidateCoefficientCache()`
  手动失效系数缓存 (admin 调价后)。
- 计量单位 **ETU** (Equivalent Token Unit) 替代单池 raw token: 桶按
  `input_coef × in + output_coef × out + cache_read_coef × cacheRead` 折算扣减。
- GENERIC 通配桶 (注册/邀请/月度) 受 `AllowedModelsJSON` 白名单限制, 防白嫖刷高价模型。
- 灰度兼容: 老 entitlement 无桶时 `ListBuckets` 返回空, Chat 仍走 legacy 单池, 不破。
- 相关说明见 §4.4 "V29: Per-Model 桶计费" 与 §6 "V29 Per-Model 桶"。
- ⚠️ **Breaking — `ModelCapabilities.SupportsDeepThinking` 字段移除**
  - **背景**: v0.9.0 引入此字段, 但全 SDK 0 处行为消费 (仅 docs 示例引用),
    backend `ModelCapabilities` 也从未声明该字段, 实际是单边死字段。
  - **迁移**: 全部改用 `SupportsMaxEffort`。两者语义等价 ("是否支持 thinking_level=max
    强度档"), `SupportsMaxEffort` 是 SDK 唯一行为字段 (`adapter_anthropic.go:182`)。
  - **判断逻辑示例**:
    ```go
    // 旧 (v0.15.x 及之前)
    if caps.SupportsDeepThinking { ... }
    // 新 (v0.16.0+)
    if caps.SupportsMaxEffort { ... }
    ```
- ⚠️ **语义修正 — `SupportsMaxEffort` 注释**
  - 旧注释 "Opus 4.6 独有" 误导, 实际字段语义是"模型支持 thinking_level=max
    请求"; DeepSeek-V4-Pro/Flash (官方 anthropic 兼容端点) 经 gateway
    `EffortHandling=ToOutputConfig` 翻译后完全可用。注释更新为中性描述。
  - 行为零变化, 老代码无须改动 (字段值由后端 `/managed-models` 端点驱动)。

### v0.15.x

> 本节只保留 SDK 使用者最需要关心的兼容点与破坏性变更。更细的网关实现背景、审计过程和分阶段交付记录，建议查主仓架构文档。

### v0.15.2 (2026-04-28) — `StripEphemeral` thinking 硬豁免 (历史污染兜底)

> **Bug fix (0 破坏性)**: `sanitize.StripEphemeral` 内置 thinking / redacted_thinking 硬豁免, 即使携带 `acosmi_ephemeral=true` 也不剥。修复 extended thinking + tool_use 续轮场景下 SDK 误剥 thinking 块导致的上游 400。

**根因**: v0.13 ~ v0.15.1 期间, 网关 `anthropic_official` preset 把 `BlockThinking` / `BlockRedactedThinking` 列入 `EphemeralResponseBlocks`, 给响应注入 `acosmi_ephemeral=true`。客户端 SDK `StripEphemeral` 在下一轮请求前据此剥除 → 上游报:

```
invalid_request_error: The `content[].thinking` in the thinking mode
must be passed back to the API.
```

实际契约: Anthropic extended thinking + tool_use 续轮**强制要求** assistant 历史中保留原始 thinking 块。纯文本续轮也接受 thinking 回传。

**修复**:

- 网关侧 (commit 55fe8090, 已部署): 移除 thinking / redacted_thinking 的 ephemeral 注入。
- SDK 侧 (本版): `StripEphemeral` 在剥除前先按 `block.type` 短路, thinking / redacted_thinking 永不剥。即使老网关或第三方工具链注入了 `acosmi_ephemeral=true` 标记, SDK 也兜底保留, 杜绝历史污染会话再次触发 400。

**对调用方可见面**:

- 公共 API 签名 0 改动 (`StripEphemeral` / `SetAutoStripEphemeralHistory` 行为对其他 block 类型不变)
- 持续会话历史会多带 thinking 块 (调用方需自行衡量是否手动裁剪节省 token; Anthropic 不计费输入 thinking token)
- `server_tool_use` / `mcp_tool_use` / 自定义 ephemeral 业务块的剥除行为不变

**测试**: 新增 3 个回归用例 (`sanitize/history_test.go`):
- `TestStripEphemeral_NeverStripsThinking` — thinking 带标记仍保留, 同轮 ephemeral text 仍剥
- `TestStripEphemeral_NeverStripsRedactedThinking` — 同上, redacted_thinking
- `TestStripEphemeral_ThinkingDoesNotCascade` — thinking 不进 droppedToolUseIDs 收集, 不联动剥 user 轮 tool_result

`sanitize_bridge_test.go:TestApplyRequestSanitizers_AutoStripEphemeral` 期望同步反转。`go test -race -count=1 ./...` 全绿; `FuzzSanitize` 1M execs / 15s -race 无 panic。

**npm**: `@acosmi/crabclaw-skill` 同步 0.15.2。

### v0.15.1 (2026-04-27) — `ensureToken` 三态等待 (启动期并发修复)

> **Bug fix (0 破坏性)**: `ensureToken` 引入"等待 Login 就绪"中间态, 修复启动期 fan-out 调用的 `not authorized` 误报。已授权场景零额外开销, 未授权场景错误信息保留。

**根因**: v0.15.0 及之前 `ensureToken` 仅有 nil → 立即报错 / 有效 → 返回 二态机。启动期同时触发 `Login` + 多个 API 调用 (CrabClaw 典型 fan-out: ws / ListModels / GetBalance / harness handshake) 各自命中 `c.tokens == nil` 立即报 `not authorized, call Login() first`, 4 条无效 WARN。

**修复**: 新增 `tokenReady chan` + `loginInFlight bool` + `tokenOnce sync.Once`:
- `loginInternal` 入口锁内置 `loginInFlight=true`, 完成后 `tokenOnce.Do(close(tokenReady))`, defer 翻 false
- `Logout` 锁内重置 `tokenReady = make(chan)` + `tokenOnce = sync.Once{}`
- `ensureToken` 锁内三快照 (tokens / ready / inFlight), 按 §4.2 三态语义分流

**对调用方可见面**:
- 已授权场景: tokenReady 已 close, 立即放行, 零额外开销 (无新分配/无锁等待)
- Login 并发场景: 自动等待至 token 就绪, 不再 4 条 WARN
- 未授权场景: 错误信息保留 `call Login() first` (调用方 fail-fast 行为不变)
- ctx 超时: 返 `waiting for token: context deadline exceeded`, `errors.Is(err, context.DeadlineExceeded)` 链兼容

**API 兼容**: 公共方法签名 0 改动 (Login / Logout / IsAuthorized / GetTokenSet / 所有业务 API)。Tauri/Rust wrapper 字符串匹配 0 破坏。

**测试**: 新增 7 个回归用例 (`ensure_token_wait_test.go`) — fail-fast / 4 并发等待 / ctx 提前到期 / 预加载零等待 / Logout 重置链路 / Login+Logout race (50 轮压测) / 等待中 Logout 边界。`go test -race -count=1 ./...` 全绿。

**深度审计修正** (实施期): 复核发现 step 5 close 在 `c.mu.Unlock` 后裸读 `c.tokenOnce` / `c.tokenReady`, 与 Logout 锁内重置构成 data race (race detector 必抓)。修复方式: 把 `tokenOnce.Do(close)` 收进同一把 Lock, 与 `c.tokens = tokens` 合并临界区。

> 完整三态语义与红线见 [§4.2 并发授权语义](#42-授权)。

### v0.15 (2026-04-27) — L6 SDK retry policy + V2 P1 错误类型化

> **新功能 (opt-in, 0 破坏性)**: SDK 端引入 `RetryPolicy` 与结构化错误类型 `HTTPError` / `NetworkError`. **默认配置 `RetryPolicy: nil` 退化到 v0.14.1 行为**, 升级到 v0.15 后老调用方零行为变化.
>
> **计费安全红线**: `defaultSafeToRetry` POST/PUT/DELETE/PATCH 默认 `false`, chat/messages/upload POST **永不重试** (双扣绝不发生); 仅 GET/HEAD/OPTIONS 默认享受 2x retry. 详见下文.

#### V2 P1 — 结构化错误类型 (`*HTTPError` + `*NetworkError`)

老 `parseHTTPError` 返回 `fmt.Errorf("HTTP %d: %s", ...)` 字符串错误, `errors.As` 出不来分类. 网络层 `c.http.Do` 错误 (timeout/EOF/connection reset) 直接 `*net.OpError` 透传, 无统一封装. v0.15 加结构化包装:

```go
// HTTPError - 5xx/4xx 业务错误
var he *acosmi.HTTPError
if errors.As(err, &he) {
    if he.StatusCode == 429 && he.RetryAfter > 0 {
        time.Sleep(time.Duration(he.RetryAfter) * time.Second)
    }
}

// NetworkError - 传输层 (timeout/EOF/connection refused)
var ne *acosmi.NetworkError
if errors.As(err, &ne) {
    if ne.IsTimeout() { /* 超时重试逻辑 */ }
    if ne.IsEOF() { /* 连接断开 */ }
}
```

**字段集**:
- `HTTPError`: `StatusCode int / Type string / Message string / RetryAfter int (秒) / Body string`
- `NetworkError`: `Op string / URL string / Cause error / Timeout bool / EOF bool` + `Unwrap() error` (`errors.Is` 链兼容)

**文案兼容承诺**: `Error()` 输出与老 `fmt.Errorf` 完全一致 (`HTTP %d: [%s] %s` / `HTTP %d: %s` / `HTTP %d` 三态). Tauri/Rust wrapper 字符串匹配 0 破坏.

**SDK 内部改动** (v0.15 已集成, 调用方透明):
- `parseHTTPError` 改返回 `*HTTPError` (新增 `parseHTTPErrorWithHeader` 自动解析 `Retry-After` 头)
- `c.doRequest(req)` helper 包装 `c.http.Do` 错误为 `*NetworkError` (`classifyTransport` 分类 ctx.DeadlineExceeded / io.EOF / "connection reset" / `net.Error.Timeout()`)
- 7 处 `parseHTTPError` 调用全部升级用 `parseHTTPErrorWithHeader` 接 `resp.Header`
- 6 处 `c.http.Do` 调用全部走 `c.doRequest` (chatStream / DownloadSkill / UploadSkill / doJSONFullInternal / doPublicJSON 等)

#### L6 — RetryPolicy

```go
client, _ := acosmi.NewClient(acosmi.Config{
    ServerURL:   "https://acosmi.com",
    RetryPolicy: acosmi.DefaultRetryPolicy, // 启用 — chat 类 POST 仍 0 retry, GET 类得 2x 稳定性
    // 或: RetryPolicy: nil — 禁用, 退化到 v0.14.1 行为
})
```

**`DefaultRetryPolicy` 字段**:

| 字段 | 默认值 | 含义 |
|---|---|---|
| `MaxAttempts` | 2 | 总尝试次数 (含首次); 1 = 不重试 |
| `Backoff` | 200ms | 首次重试退避 |
| `BackoffMax` | 2s | 退避封顶 |
| `BackoffMul` | 2.0 | 指数倍数 (200ms → 400ms → 800ms → 1.6s → 2s cap) |
| `OnRetryable` | `defaultRetryable` | 错误层闸门 |
| `SafeToRetry` | `defaultSafeToRetry` | 请求层闸门 — **计费安全红线** |

**`defaultSafeToRetry` 判定** (计费安全):

| Method | 默认 | 说明 |
|---|---|---|
| GET / HEAD / OPTIONS | `true` | 天然幂等 |
| POST / PUT / DELETE / PATCH | `false` | 双扣保护 — chat/messages/upload 永不重试 |

> 自定义 `SafeToRetry` 可对特定只读 POST 端点放行, 但**严禁**让 chat/messages POST 通过, 否则双扣.

**`defaultRetryable` 判定** (错误层):

```
*StreamError       → false (V2 P0 流已部分写出, 重试 = 双 token + 重复消息)
context.Canceled   → false (用户主动 abort)
*HTTPError 5xx/429 → true
*NetworkError IsTimeout()/IsEOF() → true
其他 (4xx/DNS/未知) → false
```

**Retry-After 头优先**: HTTPError 含 `RetryAfter > 0` 时, 退避用 `Retry-After` 秒数 (硬上限 60s 防恶意服务器卡死), 否则走指数退避.

**红线 (硬保证)**:
1. POST 默认 SafeToRetry=false → chat/messages 用户**0 行为变化**
2. Stream 路径 (`chatStream` / `chatMessagesStream`) **不调用** retry helper, 流式重试不存在
3. `*StreamError` 经 `OnRetryable` 显式排除
4. `ctx.Canceled` (用户 Ctrl+C) 立即返回, 不重试
5. 401 refresh 是 inner loop, **不算 attempt** (refresh 后重进 retry 顶)
6. 已 wrap fmt.Errorf 的 caller 通过 `errors.As` 仍可解开 `HTTPError` / `NetworkError`

**生效面**:

| 路径 | 是否走 `doRequestWithRetry` | retry 实际触发 |
|---|---|---|
| `doJSONFullInternal` (POST/GET 通用) | ✅ | GET 类 5xx/429 (POST SafeToRetry=false 单次) |
| `doPublicJSON` (匿名/公共端点) | ✅ | GET 类 5xx/429 |
| `UploadSkill` (POST multipart) | ✅ | **永不重试** (POST SafeToRetry=false), 升级仅为统一调用模式 + 错误类型化 |
| `chatStream` / `chatMessagesStream` (SSE) | ❌ | 流式硬编码绕过, 重试 = 双 token |
| `DownloadSkill` (GET 大文件) | ❌ | 老路径保留 `*RateLimitError` 兼容 (类型不一致风险), 不升级 |

**回退**: `Config{RetryPolicy: nil}` 即退化到 v0.14.1 行为.

### v0.13.x 服务端 (2026-04-27) — DeepSeek-anthropic 三参数闭环

> 网关侧 capability 闸门 + `/anthropic` 端 `response_format` 通道修补, 对 SDK 用户**0 破坏性变更**. SDK 自身代码 0 改动, 仅文档 (`§4.3 DeepSeek-anthropic 接入`) 增加 compat 模式接入指南.

**根因**: DeepSeek 在 Anthropic 兼容端点扩展三个私有字段 (`thinking` / `output_config.effort` / `response_format`). 修补前 `response_format` 在 `AnthropicProxyRequest.ShouldBindJSON` 阶段被 Gin 静默丢弃, 即使 SDK 通过 ExtraBody 注入也到不了上游。

**网关改动** (commit 待提交):
- `gateway/capability/capability.go` 新增 `SupportsOutputConfig` / `SupportsResponseFormat` 字段
- `gateway/sanitizer/headers.go` 按 capability 闸门剥除不支持 provider 的字段, 防 400
- `presets/deepseek.go` 双开 `SupportsOutputConfig=true` + `SupportsResponseFormat=true`
- `presets/{deepseek_openai,openai_compat,openai_compat_custom,dashscope_openai,zhipu_openai,volcengine_openai}.go` 显式 `SupportsResponseFormat=true` (OpenAI-wire 原生)
- `model.AnthropicProxyRequest` 新增 `ResponseFormat` 字段 + `ToChatProxyRequest` 复制
- `service/model_gateway.go` 新增 `adaptAnthropicDeepSeek` 专属适配器, dispatch 仅对 DeepSeek + AnthropicFormat 启用; 其他 Anthropic-wire provider 保持 `adaptAnthropic` 纯净路径

**对 SDK 调用方可见面**:
- `/api/v4/managed-models/<deepseek-id>/anthropic` 端点开始接受 `response_format: {type:"json_object"}` 请求体, 上游 DeepSeek 返回 JSON
- 同字段发到 Anthropic-official / DashScope-anthropic / Zhipu-anthropic / OpenRouter / third-party 仍被网关 sanitizer 自动剥除 (双层防御), 不会 400
- SDK 高级 API (`Thinking.Level` / `OutputConfig{Format,Schema}`) 在 DeepSeek-anthropic 上**未自动适配**, 见 `§4.3 DeepSeek-anthropic 接入` compat 模式
- 计划 v0.14 引入 SDK provider-aware adapter 自动翻译, 届时高级 API 在 DeepSeek 上即可正确生效

### v0.14.x 服务端 (2026-04-27) — 长远项 L1 / L3 / L7 落地

> 网关与服务端基建升级, 对 SDK 用户**0 破坏性变更**. SDK 自身代码 0 改动 (HEAD `v0.14.1`, `0931b49`). 本节列出三项基建对 SDK 调用方的可见契约面, 帮助调用方应用网关侧能力.
>
> 范围澄清: 本批仅落 **L1 / L3 / L7** 三项 (其中 L3 含 PR1 runstatus 包 + PR2 5 model 字段类型化). L2 alert / L4 OTel / L5 多凭证 failover / **L6 SDK 内置 retry policy** 均**未实施**, 后续版本独立推进.

#### L1 — 网关错误码细化 (后端 `pkg/errkind` + `pkg/transport.Do`)

后端新增 `pkg/errkind/` (15 Kind 物理出 `service/gateway/errors`, 27 case 透明) + `pkg/transport.Do` (含 `SingleRetry` 200ms 单次退避, `DefaultRetryBackoff` 常量). **62 处** outbound HTTP 全部迁 `transport.Do` (实测 grep 命中, 含 handler/adk/auth/plugin/workflow/storage/chat/multimodal/code_interpreter/skill/notification/mcp/client/service 全子树).

**对 SDK 调用方可见面** (在 v0.14.1 已发布, 此处汇总):
- `*StreamError.Code` / `errors.As` 可获 5 个新 transport kind, 与 v0.14.1 错误码表一致:
  - `upstream_timeout` / `upstream_unreachable` / `upstream_disconnect` / `upstream_malformed` / `client_canceled`
- 网关侧透明 200ms 单次重试吃 80%+ 瞬断 — SDK 调用方无需自行重试 GET 类查询 (但**计费类 POST 仍不重试**, 见 v0.14.1 段)
- 错误文本 (`*StreamError.Error()`) 严格保留, Tauri/Rust 字符串匹配兼容

**SDK 端不变**: 三处 Do (`chatStreamInternal` L761 / `doJSONFullInternal` L1638 / `doPublicJSON` L1737) 仍是 401 单次 refresh 模式, **未引入 RetryPolicy / SafeToRetry / 指数退避** — 该项 (L6) 后续 v0.15 独立推进.

#### L3 — 状态字段字面量契约 (后端 `pkg/runstatus` 6 域)

后端新增 `pkg/runstatus/` (6 域命名 string 类型 + `CanTransition` 状态机) + L3.PR2 5 个 model 字段从 `string` 升级为 `runstatus.Status`: `WorkflowRun.Status` / `WorkflowRunStep.Status` / `ConsumeRecord.Status` / `PluginExecutionLog.Status` / `ManagedModelUsageLog.Status` (后者 L3.PR2 之前已迁).

**对 SDK 调用方可见面**: **0 行为变化**. `runstatus.Status` 是 `string` 底层命名类型, JSON marshal/unmarshal / DB 字面量 / SSE 协议字段全部不变. 服务端 GORM 自动 scan/value, 跨 Java 边界透传 `json.RawMessage` 不解析 Status.

**状态字段字面量契约表** (跨版本稳定保证):

| 域 | 端点 | 字段 | 字面量集 |
|---|---|---|---|
| Workflow | `GET /api/v4/workflow/runs/:id` | `status` | `pending` / `running` / `completed` / `failed` / `cancelled` |
| WorkflowStep | 同上 (steps[]) | `status` | `pending` / `running` / `completed` / `failed` / `skipped` |
| ConsumeRecord | SSE `managed-model.v2` event | `consumeStatus` | `HELD` / `PENDING_SETTLE` / `SETTLED` / `RELEASED` |
| PluginExec | `GET /api/v4/admin/logs?type=plugin` | `status` | `SUCCESS` / `FAILED` (DB 原值) → 看板映射 `success` / `error` |
| Gateway | `GET /api/v4/admin/logs?type=managed-model` | `status` | `success` / `error` / `timeout` / `pending_settle` / `empty_response` / `upstream_timeout` / `upstream_unreachable` / `upstream_disconnect` / `upstream_malformed` / `client_canceled` |
| AppExec | `GET /api/v4/admin/logs?type=app` | `status` | `pending` / `running` / `waiting` / `completed` / `failed` / `cancelled` |

**注意大小写**: ConsumeRecord 与 PluginExec 是大写 (跨 Java 兼容 / 插件审核体系沿用), 其余是小写. SDK 解析时严格按字面量比较, **不要做 case-insensitive 转换**.

**附加修复**: `handler/plugin_health.go` 4 处 `fmt.Errorf("...: %v", err)` → `%w` (errors.As 链可见性恢复). 错误文本完全一致, 字符串匹配兼容. SDK 端无变化.

#### L7 — 服务端测试基建 (后端 `pkg/testutil/flaky`)

后端新增 `pkg/testutil/flaky/` 5 个 httptest 夹具 (`ServeAndCloseAfterBytes` / `ServeAndDelay` / `ServeChunked` / `ServeMalformed` / `ServeUnreachable`) + 10 单测, 给 V2 P0 / L1 / 后续 L5/L6 测试复用.

**对 SDK 调用方可见面**: **完全透明**. 这是服务端测试基建, 不影响 API/protocol/error 契约.

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
