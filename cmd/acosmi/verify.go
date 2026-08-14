package main

// =============================================================================
// 接入验证 (§7.2 步骤 9)
//
// 两级验证, 用的是**刚写进配置的那枚凭证**走**兼容层**——
// 这一点很关键: 我们要验证的不是"我的 SDK 能不能调通", 而是"目标智能体照着这份配置
// 能不能调通"。所以必须用同一个 base_url、同一个 wire、同一枚 key, 不能用 SDK 的
// OAuth 通道抄近路验证 —— 那验的是另一条链路。
//
//   预检: GET {base_url}/models      不计费, 证明凭证与路由通
//   实测: 一次最短推理 (max_tokens=1) 证明资金链与模型准入也通; --no-verify 可跳过
//
// 实测会真的花掉极少量额度 —— 这是刻意的: "配好了但一调就 402" 是最糟的交付,
// 让用户在 add 的当场就知道, 远好过第二天在自己的智能体里撞见。
// =============================================================================

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

var verifyHTTPClient = &http.Client{Timeout: 60 * time.Second}

// verifyAgent 预检 + (可选) 最小推理。
func verifyAgent(ctx context.Context, entry *AgentEntry, apiKey string, runInference bool) error {
	if err := verifyCatalog(ctx, entry, apiKey); err != nil {
		return err
	}
	fmt.Println("[✓] 目录预检通过 (凭证与路由可用)")
	if !runInference {
		fmt.Println("[i] 已跳过最小推理验证 (--no-verify)")
		return nil
	}
	if err := verifyInference(ctx, entry, apiKey); err != nil {
		return err
	}
	fmt.Println("[✓] 最小推理验证通过 (资金链与模型准入可用)")
	return nil
}

func verifyCatalog(ctx context.Context, entry *AgentEntry, apiKey string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, entry.BaseURL+"/models", nil)
	if err != nil {
		return fmt.Errorf("构造预检请求失败: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+apiKey)
	resp, err := verifyHTTPClient.Do(req)
	if err != nil {
		return fmt.Errorf("预检请求失败 (检查网络与 base_url %s): %w", entry.BaseURL, err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 8192))
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("预检返回 %d: %s", resp.StatusCode, describeCompatError(body))
	}
	return nil
}

func verifyInference(ctx context.Context, entry *AgentEntry, apiKey string) error {
	var url string
	var payload map[string]any
	if entry.Protocol == "anthropic" {
		url = entry.BaseURL + "/messages"
		payload = map[string]any{
			"model":      entry.ModelID,
			"max_tokens": 1,
			"messages":   []map[string]any{{"role": "user", "content": "hi"}},
		}
	} else {
		url = entry.BaseURL + "/chat/completions"
		payload = map[string]any{
			"model":      entry.ModelID,
			"max_tokens": 1,
			"messages":   []map[string]any{{"role": "user", "content": "hi"}},
		}
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("构造验证请求失败: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(raw))
	if err != nil {
		return fmt.Errorf("构造验证请求失败: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+apiKey)
	if entry.Protocol == "anthropic" {
		// Anthropic 客户端惯例; 服务端两种都接受, 这里按目标协议的常见形态发,
		// 顺带把 x-api-key 这条路也验一遍。
		req.Header.Set("x-api-key", apiKey)
		req.Header.Set("anthropic-version", "2023-06-01")
	}

	resp, err := verifyHTTPClient.Do(req)
	if err != nil {
		return fmt.Errorf("验证调用失败: %w", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 8192))
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("验证调用返回 %d: %s", resp.StatusCode, describeCompatError(body))
	}
	return nil
}

// describeCompatError 从原生错误信封里抽出人能看懂的一句话。
// 两种协议的信封形状不同, 但都把关键信息放在 error.message 里。
func describeCompatError(body []byte) string {
	var envelope struct {
		Error struct {
			Type    string `json:"type"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(body, &envelope); err == nil && envelope.Error.Message != "" {
		if envelope.Error.Type != "" {
			return fmt.Sprintf("%s: %s", envelope.Error.Type, envelope.Error.Message)
		}
		return envelope.Error.Message
	}
	trimmed := strings.TrimSpace(string(body))
	if len(trimmed) > 300 {
		trimmed = trimmed[:300] + "…"
	}
	if trimmed == "" {
		return "(空响应)"
	}
	return trimmed
}
