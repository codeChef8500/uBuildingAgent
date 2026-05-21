package llmprovider

// defaultThinkingBudgets — 各 ThinkingLevel 的默认 token 预算
var defaultThinkingBudgets = ThinkingBudgets{
	Minimal: 1024,
	Low:     2048,
	Medium:  8192,
	High:    16384,
}

// minOutputTokens — thinking 场景下保留给 output 的最小 token 数
const minOutputTokens = 1024

// AdjustMaxTokensForThinking 计算 thinking 场景下的 maxTokens 和 thinkingBudget
//
// 对应 Pi packages/ai/src/providers/simple-options.ts adjustMaxTokensForThinking()
//
// 参数：
//   - baseMaxTokens: 调用方显式指定的 max tokens（0 = 未指定，使用 modelMaxTokens）
//   - modelMaxTokens: 模型支持的最大 output tokens
//   - level: 推理强度（"xhigh" 自动降级为 "high"）
//   - budgets: 自定义 token 预算（nil = 使用默认值）
//
// 返回：
//   - maxTokens: 发送给 provider 的 max_tokens 值
//   - thinkingBudget: 发送给 provider 的 thinking budget 值
func AdjustMaxTokensForThinking(
	baseMaxTokens int,
	modelMaxTokens int,
	level ThinkingLevel,
	budgets *ThinkingBudgets,
) (maxTokens int, thinkingBudget int) {
	// "xhigh" 降级到 "high"（对应 Pi clampReasoning）
	level = ClampThinkingLevel(level)

	// 合并默认预算与自定义预算
	b := mergeBudgets(budgets)

	budget := budgetForLevel(b, level)

	var max int
	if baseMaxTokens == 0 {
		// 调用方未指定上限，用模型最大值（对应 Pi: baseMaxTokens === undefined）
		max = modelMaxTokens
	} else {
		// 调用方指定了上限：向上扩展以容纳 thinking budget，但不超过模型上限
		max = baseMaxTokens + budget
		if max > modelMaxTokens {
			max = modelMaxTokens
		}
	}

	// 若 max 不足以容纳 thinking budget + 最小 output，缩小 budget
	// 对应 Pi: if (maxTokens <= thinkingBudget) { thinkingBudget = Math.max(0, maxTokens - minOutputTokens) }
	if max <= budget {
		budget = max - minOutputTokens
		if budget < 0 {
			budget = 0
		}
	}

	return max, budget
}

// ClampThinkingLevel 将 "xhigh" 降级为 "high"
// 对应 Pi clampReasoning()；不支持 "xhigh" 的 provider 调用此函数规范化输入
func ClampThinkingLevel(level ThinkingLevel) ThinkingLevel {
	if level == ThinkingLevelXHigh {
		return ThinkingLevelHigh
	}
	return level
}

// mergeBudgets 将自定义预算与默认预算合并（自定义值覆盖默认值，0 值保留默认）
func mergeBudgets(custom *ThinkingBudgets) ThinkingBudgets {
	b := defaultThinkingBudgets
	if custom == nil {
		return b
	}
	if custom.Minimal > 0 {
		b.Minimal = custom.Minimal
	}
	if custom.Low > 0 {
		b.Low = custom.Low
	}
	if custom.Medium > 0 {
		b.Medium = custom.Medium
	}
	if custom.High > 0 {
		b.High = custom.High
	}
	return b
}

// budgetForLevel 返回指定 level 的 token 预算
// 前置条件：level 已经过 ClampThinkingLevel（不含 "xhigh"）
func budgetForLevel(b ThinkingBudgets, level ThinkingLevel) int {
	switch level {
	case ThinkingLevelMinimal:
		return b.Minimal
	case ThinkingLevelLow:
		return b.Low
	case ThinkingLevelMedium:
		return b.Medium
	case ThinkingLevelHigh:
		return b.High
	default:
		return 0
	}
}
