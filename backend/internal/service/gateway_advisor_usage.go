package service

import (
	"context"
	"strings"
	"time"

	"github.com/tidwall/gjson"
)

const (
	usageIterationTypeAdvisor = "advisor_message"
	// advisorModelMaxLength 与 usage_logs.advisor_model VARCHAR(100) 一致；模型名来自上游响应，
	// 超长会让整行用量写入失败，所以入库前截断。
	advisorModelMaxLength = 100
)

// AdvisorUsage 汇总 usage.iterations[] 里 type=advisor_message 的子推理用量。
// Anthropic 顶层 usage 只含执行模型（各 type=message 迭代之和），advisor 子推理按其自身模型
// 单独计费，只出现在 iterations 中。一次请求只声明一个 advisor 工具，因此只有一个 advisor 模型。
type AdvisorUsage struct {
	Model                    string
	Calls                    int
	InputTokens              int
	OutputTokens             int
	CacheCreationInputTokens int
	CacheReadInputTokens     int
	CacheCreation5mTokens    int
	CacheCreation1hTokens    int
}

// HasUsage 判断本次请求是否发生过 advisor 调用。
func (a AdvisorUsage) HasUsage() bool {
	return a.Calls > 0
}

// applyAdvisorUsageFromJSON 从 usage 节点读取 iterations 并覆盖 usage.Advisor。
// 流式 message_delta 每次携带完整的最新 iterations 数组，所以整体替换而不是累加；
// 没有 iterations 字段时保持原值。
func applyAdvisorUsageFromJSON(usage *ClaudeUsage, usageNode gjson.Result) {
	if usage == nil {
		return
	}
	iterations := usageNode.Get("iterations")
	if !iterations.IsArray() {
		return
	}
	advisor := AdvisorUsage{}
	iterations.ForEach(func(_, it gjson.Result) bool {
		if it.Get("type").String() != usageIterationTypeAdvisor {
			return true
		}
		advisor.Calls++
		if model := truncateAdvisorModel(it.Get("model").String()); model != "" {
			advisor.Model = model
		}
		advisor.InputTokens += int(it.Get("input_tokens").Int())
		advisor.OutputTokens += int(it.Get("output_tokens").Int())
		advisor.CacheCreationInputTokens += int(it.Get("cache_creation_input_tokens").Int())
		advisor.CacheReadInputTokens += int(it.Get("cache_read_input_tokens").Int())
		advisor.CacheCreation5mTokens += int(it.Get("cache_creation.ephemeral_5m_input_tokens").Int())
		advisor.CacheCreation1hTokens += int(it.Get("cache_creation.ephemeral_1h_input_tokens").Int())
		return true
	})
	usage.Advisor = advisor
}

func truncateAdvisorModel(model string) string {
	model = strings.TrimSpace(model)
	if runes := []rune(model); len(runes) > advisorModelMaxLength {
		model = string(runes[:advisorModelMaxLength])
	}
	return model
}

// advisorCost 按 advisor 模型自身价格计算子推理费用（与主请求同一分组、倍率与计价时刻）。
// 没有 advisor 调用或未声明模型时返回 nil。
func (s *GatewayService) advisorCost(ctx context.Context, result *ForwardResult, apiKey *APIKey, multiplier float64, pricingAt time.Time) *CostBreakdown {
	advisor := result.Usage.Advisor
	if !advisor.HasUsage() || advisor.Model == "" {
		return nil
	}
	// service tier（fast/priority）与 reasoning effort 只作用于执行模型，不传给 advisor。
	sub := &ForwardResult{
		Model: advisor.Model,
		Usage: ClaudeUsage{
			InputTokens:              advisor.InputTokens,
			OutputTokens:             advisor.OutputTokens,
			CacheCreationInputTokens: advisor.CacheCreationInputTokens,
			CacheReadInputTokens:     advisor.CacheReadInputTokens,
			CacheCreation5mTokens:    advisor.CacheCreation5mTokens,
			CacheCreation1hTokens:    advisor.CacheCreation1hTokens,
		},
	}
	return s.calculateTokenCost(ctx, sub, apiKey, advisor.Model, multiplier, pricingAt)
}

// addAdvisorCost 把 advisor 费用叠加到请求费用上，并单独记下 advisor 部分。
func addAdvisorCost(cost *CostBreakdown, advisor *CostBreakdown) *CostBreakdown {
	if advisor == nil || (advisor.TotalCost == 0 && advisor.ActualCost == 0) {
		return cost
	}
	if cost == nil {
		cost = &CostBreakdown{}
	}
	cost.TotalCost += advisor.TotalCost
	cost.ActualCost += advisor.ActualCost
	cost.AdvisorCost += advisor.TotalCost
	return cost
}
