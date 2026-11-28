import { describe, expect, it, vi } from 'vitest'
import { mount } from '@vue/test-utils'

import UsageStatsCards from '../UsageStatsCards.vue'

const messages: Record<string, string> = {
  'usage.totalRequests': 'Total Requests',
  'usage.inSelectedRange': 'in selected range',
  'usage.totalTokens': 'Total Tokens',
  'usage.in': 'In',
  'usage.out': 'Out',
  'usage.cacheTotal': 'Cache',
  'usage.cacheBreakdown': 'Cache Token Breakdown',
  'usage.cacheCreationTokensLabel': 'Cache Creation',
  'usage.cacheReadTokensLabel': 'Cache Read',
  'usage.totalCost': 'Total Cost',
  'usage.accountCost': 'Cost',
  'usage.standardCost': 'Standard',
  'usage.avgDuration': 'Avg Duration',
}

vi.mock('vue-i18n', async () => {
  const actual = await vi.importActual<typeof import('vue-i18n')>('vue-i18n')
  return {
    ...actual,
    useI18n: () => ({
      t: (key: string) => messages[key] ?? key,
    }),
  }
})

const stats = {
  total_requests: 1,
  total_input_tokens: 100,
  total_output_tokens: 50,
  total_cache_tokens: 34,
  total_cache_creation_tokens: 12,
  total_cache_read_tokens: 22,
  total_tokens: 184,
  total_cost: 0.001,
  total_actual_cost: 0.001,
  total_account_cost: 0.001,
  average_duration_ms: 250,
}

describe('UsageStatsCards', () => {
  it('shows cache token breakdown values', () => {
    const wrapper = mount(UsageStatsCards, {
      props: {
        stats,
      },
      global: {
        stubs: {
          Icon: true,
          Teleport: true,
        },
      },
    })

    const text = wrapper.text()
    expect(text).toContain('Cache: 34')
    expect(text).toContain('Cache Token Breakdown')
    expect(text).toContain('Cache Creation')
    expect(text).toContain('12')
    expect(text).toContain('Cache Read')
    expect(text).toContain('22')
  })

  it('keeps the cache tooltip out of the layout while it is hidden', () => {
    const wrapper = mount(UsageStatsCards, {
      props: {
        stats,
      },
      global: {
        stubs: {
          Icon: true,
          Teleport: true,
        },
      },
    })

    // 浮层挂到 body 且默认 display: none，固定宽度不会撑宽窄屏页面
    const tooltip = wrapper.get('[role="tooltip"]')
    expect(tooltip.isVisible()).toBe(false)
  })

  it('hides the advisor table when no advisor usage exists', () => {
    const wrapper = mount(UsageStatsCards, {
      props: { stats },
      global: { stubs: { Icon: true } },
    })

    expect(wrapper.text()).not.toContain('usage.advisor.title')
  })

  it('lists advisor usage per advisor model', () => {
    const wrapper = mount(UsageStatsCards, {
      props: {
        stats: {
          ...stats,
          advisors: [{
            model: 'claude-fable-5-2',
            requests: 3,
            calls: 4,
            input_tokens: 311423,
            output_tokens: 10019,
            cache_creation_tokens: 0,
            cache_read_tokens: 0,
            cost: 2.892144,
            actual_cost: 3.1813584,
          }],
        },
      },
      global: { stubs: { Icon: true } },
    })

    const text = wrapper.text()
    expect(text).toContain('usage.advisor.title')
    expect(text).toContain('claude-fable-5-2')
    expect(text).toContain('311.42K')
    expect(text).toContain('10.02K')
    expect(text).toContain('$3.1814')
  })
})
