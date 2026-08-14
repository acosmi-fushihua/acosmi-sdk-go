package acosmi

// 分组 Scope — 与后端 DesktopOAuthScopes 保持一致 (V2: 10→3 合并)
const (
	ScopeAI      = "ai"      // 模型服务: 模型调用 + 流量包 + 权益
	ScopeSkills  = "skills"  // 技能与工具: 技能商店 + 工具列表 + 执行
	ScopeAccount = "account" // 账户信息: 个人资料 + 钱包余额 + 交易记录
)

// 会员委托 Key 控制面 scope (2026-08-14 开放 API 方案)。
//
// 高风险且**刻意不进 AllScopes()**: 持有它即可签发"长期代表本人会员权益调用模型"的静态凭证,
// 桌面登录不应自动获得该能力 —— 调用方必须显式申请, 用户才会在同意页看到这项授权。
// 服务端约束 (三条一起才闭合, 见 desktop_oauth.go 注释): 不并入任何分组展开、不得签进 sk- Key、
// 签发接口只接受有同意页的 desktop OAuth 或同源登录态。
const ScopeAgentAccessManage = "agent_access:manage"

// Deprecated: 旧细粒度 scope, 保留向后兼容, 新代码请用分组 scope
const (
	ScopeModels        = "models"
	ScopeModelsChat    = "models:chat"
	ScopeEntitlements  = "entitlements"
	ScopeTokenPackages = "token-packages"
	ScopeSkillStore    = "skill_store"
	ScopeTools         = "tools"
	ScopeToolsExecute  = "tools:execute"
	ScopeWallet        = "wallet"
	ScopeWalletReadonly = "wallet:readonly"
	ScopeProfile       = "profile"
)

// [RC-9] Scope 预设组改为函数, 返回新切片, 防止外部篡改

// AllScopes 全部分组 scope (推荐)
func AllScopes() []string {
	return []string{ScopeAI, ScopeSkills, ScopeAccount}
}

// ModelScopes 模型服务相关 scope
func ModelScopes() []string {
	return []string{ScopeAI}
}

// CommerceScopes 商城/钱包 scope
func CommerceScopes() []string {
	return []string{ScopeAI, ScopeAccount}
}

// SkillScopes 技能/工具 scope
func SkillScopes() []string {
	return []string{ScopeSkills}
}

// AgentAccessScopes 会员委托 Key 控制面 scope (需显式申请; AllScopes 不含本项)。
// 典型用法: acosmi.Login(ctx, name, append(acosmi.AllScopes(), acosmi.AgentAccessScopes()...))
func AgentAccessScopes() []string {
	return []string{ScopeAgentAccessManage}
}
