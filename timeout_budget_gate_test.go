package acosmi

// 结构闸门：请求推理 / 生成端点的方法必须显式声明自己的超时预算。
//
// 由来（2026-08-06）：`GenerateVideo` 一个 deadline 都不设，于是落回
// `doJSONFullInternal` 的控制面默认值 30s —— 而它的直接兄弟 `GenerateImage` 设了
// `chatRequestTimeout`。同一处错误在三个兄弟 SDK 的同一个方法上**同时**存在：
// TypeScript 的 `generateVideo` 漏传 `doJSONFullRaw` 的第 5 实参、Rust 的
// `generate_video` 传 `DEFAULT_JSON_TIMEOUT_MS`。三份独立实现犯同一个错，说明问题不在
// 人而在形状：**控制面默认值对调用点是隐式的，谁都看不见自己继承了什么。**
//
// 同轮查出第二种形态：`/skill-generator/*` 三条路由服务端用的是 120s 的 LLM 客户端
// （`internal/service/skill/generator.go`），客户端却封在 30s —— 内外预算倒挂，服务端还
// 没答完客户端就先断了。
//
// 判据刻意选**端点路径**而不是方法名：端点是「这是不是推理」的事实来源，方法名只是
// 某人当时的命名。新增端点忘了声明预算会当场红，而不是等用户在 30 秒上撞墙。

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// 推理 / 生成端点的特征。`adapter.EndpointSuffix()` 是 chat 族的构造方式（端点由
// adapter 拼出，没有字面量可扫）。
var inferenceMarkers = []string{
	"/skill-generator/",
	"/videos/generations",
	"/images/generations",
	"adapter.EndpointSuffix()",
}

// 按设计不设 deadline 的方法及理由。
//
// 流式方法**必须**在这里：SSE 是长连接，给它套 11 分钟的 deadline 会在流中途把连接
// 掐断 —— 那是比没有预算更糟的缺陷。
var noDeadlineAllowlist = map[string]string{
	"chatMessagesStreamInternal": "SSE 长连：加 deadline 会在流中途掐断",
	"chatStreamInternal":         "SSE 长连：加 deadline 会在流中途掐断",
}

// funcNameOfLine 从 `func (c *Client) Name(` 或 `func Name(` 取方法名。
func funcNameOfLine(line string) (string, bool) {
	if !strings.HasPrefix(line, "func ") {
		return "", false
	}
	rest := strings.TrimPrefix(line, "func ")
	if strings.HasPrefix(rest, "(") {
		i := strings.Index(rest, ")")
		if i < 0 {
			return "", false
		}
		rest = strings.TrimSpace(rest[i+1:])
	}
	end := strings.IndexAny(rest, "([")
	if end <= 0 {
		return "", false
	}
	return rest[:end], true
}

// auditSource 返回 (命中端点数, 违规描述)。抽成纯函数是为了能用合成源码做负向对照 ——
// 扫描器自己坏掉时闸门会恒绿，那比没有闸门更糟。
func auditSource(src, origin string) (int, []string) {
	normalized := strings.ReplaceAll(src, "\r\n", "\n")
	lines := strings.Split(normalized, "\n")

	type fnStart struct {
		at   int
		name string
	}
	var starts []fnStart
	for i, line := range lines {
		if name, ok := funcNameOfLine(line); ok {
			starts = append(starts, fnStart{i, name})
		}
	}

	seen := 0
	var bad []string
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "//") {
			continue
		}
		hit := false
		for _, m := range inferenceMarkers {
			if strings.Contains(line, m) {
				hit = true
				break
			}
		}
		if !hit {
			continue
		}
		seen++

		idx := -1
		for k, s := range starts {
			if s.at <= i {
				idx = k
			} else {
				break
			}
		}
		if idx < 0 {
			continue
		}
		end := len(lines)
		if idx+1 < len(starts) {
			end = starts[idx+1].at
		}
		name := starts[idx].name
		if _, ok := noDeadlineAllowlist[name]; ok {
			continue
		}
		body := strings.Join(lines[starts[idx].at:end], "\n")
		if !strings.Contains(body, "chatRequestTimeout") {
			bad = append(bad, origin+"::"+name+
				" 请求推理/生成端点却未声明生成预算 chatRequestTimeout；"+
				"确属控制面或流式长连请连同理由加入 noDeadlineAllowlist")
		}
	}
	return seen, bad
}

func TestEveryInferenceEndpointDeclaresABudget(t *testing.T) {
	// 递归扫全模块而不是只扫包根：本 SDK 现在是扁平的（端点全在根包的 client.go），
	// 只扫根目录**今天**没有盲区 —— 但那样一来，把端点挪进一个子包就能悄悄绕开闸门。
	// 一个有盲区的闸门会给出「已覆盖」的错觉，比没有闸门更糟。
	total := 0
	var bad []string
	files := 0
	err := filepath.WalkDir(".", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			// 构建产物与依赖目录不是本仓源码。
			if d.Name() == "vendor" || d.Name() == "testdata" || strings.HasPrefix(d.Name(), ".") && d.Name() != "." {
				return fs.SkipDir
			}
			return nil
		}
		if filepath.Ext(path) != ".go" || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		src, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		files++
		seen, v := auditSource(string(src), filepath.ToSlash(path))
		total += seen
		bad = append(bad, v...)
		return nil
	})
	if err != nil {
		t.Fatalf("遍历源码: %v", err)
	}
	if files < 20 {
		t.Fatalf("只扫到 %d 个生产源文件，疑似扫描器失效", files)
	}
	if len(bad) > 0 {
		t.Fatalf("超时预算闸门发现 %d 处违规：\n%s", len(bad), strings.Join(bad, "\n"))
	}
	// 扫描器坏掉（marker 改名、目录变化）会让它一个都找不到从而恒绿。
	if total < 9 {
		t.Fatalf("只扫到 %d 处推理端点，疑似扫描器失效（应 >= 9）", total)
	}
}

func TestGateCatchesAGenerationEndpointWithoutBudget(t *testing.T) {
	// 负向对照：这正是 GenerateVideo 修复前的形状。
	synthetic := strings.Join([]string{
		`func (c *Client) GenerateMusic(ctx context.Context, modelID string) error {`,
		`	endpoint := "/managed-models/" + modelID + "/videos/generations"`,
		`	return c.doJSONFull(ctx, http.MethodPost, endpoint, nil, nil)`,
		`}`,
	}, "\n")
	seen, bad := auditSource(synthetic, "synthetic.go")
	if seen != 1 {
		t.Fatalf("合成源码应恰有 1 处端点命中，实际 %d", seen)
	}
	if len(bad) != 1 {
		t.Fatalf("闸门应报出 1 处违规，实际 %v", bad)
	}
	if !strings.Contains(bad[0], "GenerateMusic") {
		t.Fatalf("违规描述应点名方法：%s", bad[0])
	}
}

func TestGateAcceptsAStreamingMethodWithoutDeadline(t *testing.T) {
	synthetic := strings.Join([]string{
		`func (c *Client) chatStreamInternal(ctx context.Context) error {`,
		`	endpoint := "/managed-models/" + modelID + adapter.EndpointSuffix()`,
		`	return nil`,
		`}`,
	}, "\n")
	seen, bad := auditSource(synthetic, "synthetic.go")
	if seen != 1 {
		t.Fatalf("合成源码应恰有 1 处端点命中，实际 %d", seen)
	}
	if len(bad) != 0 {
		t.Fatalf("流式方法在 allowlist 内不该被判违规：%v", bad)
	}
}
