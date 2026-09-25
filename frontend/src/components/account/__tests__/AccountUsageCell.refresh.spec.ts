import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount, type VueWrapper } from '@vue/test-utils'
import AccountUsageCell from '../AccountUsageCell.vue'
import type { Account } from '@/types'

vi.mock('@/api/admin', () => ({
  adminAPI: {
    accounts: {
      getUsage: vi.fn()
    }
  }
}))

vi.mock('vue-i18n', async () => {
  const actual = await vi.importActual<typeof import('vue-i18n')>('vue-i18n')
  return {
    ...actual,
    useI18n: () => ({
      t: (key: string) => key
    })
  }
})

type BatchOptions = { force?: boolean; bypassCache?: boolean }

const AUTO_REFRESH_INTERVAL_MS = 5_000
const ONE_MINUTE_MS = 60_000
const ACCOUNT_COUNT = 20

const stubs = {
  UsageProgressBar: true,
  AccountQuotaInfo: true,
  OpenAIQuotaResetCell: true,
  OpenAIReferralCell: true
}

function makeOpenAIAccount(id: number, overrides: Partial<Account> = {}): Account {
  return {
    id,
    name: `openai-${id}`,
    platform: 'openai',
    type: 'oauth',
    proxy_id: null,
    concurrency: 1,
    priority: 1,
    status: 'active',
    error_message: null,
    last_used_at: '2026-03-07T10:00:00Z',
    expires_at: null,
    auto_pause_on_expired: true,
    created_at: '2026-03-01T00:00:00Z',
    updated_at: '2026-03-07T10:00:00Z',
    schedulable: true,
    rate_limited_at: null,
    rate_limit_reset_at: null,
    overload_until: null,
    temp_unschedulable_until: null,
    temp_unschedulable_reason: null,
    session_window_start: null,
    session_window_end: null,
    session_window_status: null,
    extra: {
      codex_usage_updated_at: '2026-03-07T10:00:00Z',
      codex_5h_used_percent: 10,
      codex_7d_used_percent: 20
    },
    ...overrides
  }
}

function mountCell(account: Account, requestBatchedUsage: (account: Account, options?: BatchOptions) => void) {
  return mount(AccountUsageCell, {
    props: { account, manualRefreshToken: 0, requestBatchedUsage },
    global: { stubs }
  })
}

/**
 * 模拟 AccountsView 的批量用量队列与后端：
 * - 同一 tick 内的请求合并成一次批量请求，只要有一个带 force，整批就是 force；
 * - 后端 force 会跳过探测缓存、真实探测上游并回写 extra（UpdateExtra 同时刷新 updated_at）；
 * - 非 force 命中后端探测缓存，不回写。
 */
function createBatchSimulator(accountCount: number) {
  const backendRows = new Map<number, Account>()
  for (let id = 1; id <= accountCount; id += 1) backendRows.set(id, makeOpenAIAccount(id))

  const batches: Array<{ ids: number[]; force: boolean }> = []
  const pendingIds = new Set<number>()
  let pendingForce = false
  let flushTimer: ReturnType<typeof setTimeout> | null = null

  const writeBackendRow = (id: number, patch: (row: Account) => Account) => {
    const row = backendRows.get(id)
    if (row) backendRows.set(id, patch(row))
  }

  const flush = () => {
    flushTimer = null
    const ids = Array.from(pendingIds)
    const force = pendingForce
    pendingIds.clear()
    pendingForce = false
    if (ids.length === 0) return
    batches.push({ ids, force })
    if (!force) return
    const now = new Date().toISOString()
    for (const id of ids) {
      writeBackendRow(id, (row) => ({
        ...row,
        updated_at: now,
        extra: { ...row.extra, codex_usage_updated_at: now }
      }))
    }
  }

  const requestBatchedUsage = (account: Account, options?: BatchOptions) => {
    pendingIds.add(account.id)
    pendingForce = pendingForce || options?.force === true
    if (flushTimer === null) flushTimer = setTimeout(flush, 0)
  }

  const markAllUsed = () => {
    const now = new Date().toISOString()
    for (const id of backendRows.keys()) {
      writeBackendRow(id, (row) => ({ ...row, last_used_at: now, updated_at: now }))
    }
  }

  return { backendRows, batches, requestBatchedUsage, markAllUsed }
}

type Simulator = ReturnType<typeof createBatchSimulator>

async function mountAll(sim: Simulator) {
  const wrappers = new Map<number, VueWrapper>()
  for (const [id, row] of sim.backendRows) wrappers.set(id, mountCell(row, sim.requestBatchedUsage))
  await vi.advanceTimersByTimeAsync(0)
  return wrappers
}

// 模拟 AccountsView 自动刷新：拉列表，updated_at 变化的行整体替换
async function syncRows(sim: Simulator, wrappers: Map<number, VueWrapper>) {
  for (const [id, wrapper] of wrappers) {
    const next = sim.backendRows.get(id)!
    const current = wrapper.props('account') as Account
    if (current.updated_at !== next.updated_at) await wrapper.setProps({ account: next })
  }
}

async function runAutoRefreshFor(
  sim: Simulator,
  wrappers: Map<number, VueWrapper>,
  durationMs: number,
  beforeEachTick: () => void = () => {}
) {
  for (let elapsed = AUTO_REFRESH_INTERVAL_MS; elapsed <= durationMs; elapsed += AUTO_REFRESH_INTERVAL_MS) {
    await vi.advanceTimersByTimeAsync(AUTO_REFRESH_INTERVAL_MS)
    beforeEachTick()
    await syncRows(sim, wrappers)
    await vi.advanceTimersByTimeAsync(0)
  }
}

const countBatches = (sim: Simulator, fromIndex: number) => {
  const window = sim.batches.slice(fromIndex)
  return {
    force: window.filter((batch) => batch.force).length,
    normal: window.filter((batch) => !batch.force).length
  }
}

describe('AccountUsageCell 批量用量刷新', () => {
  beforeEach(() => {
    vi.useFakeTimers({ toFake: ['setTimeout', 'clearTimeout', 'setInterval', 'clearInterval', 'Date'] })
    vi.setSystemTime(new Date('2026-03-07T10:00:00Z'))
    Object.defineProperty(window, 'matchMedia', {
      writable: true,
      value: vi.fn().mockImplementation(() => ({
        matches: true,
        media: '(min-width: 768px)',
        onchange: null,
        addListener: vi.fn(),
        removeListener: vi.fn(),
        addEventListener: vi.fn(),
        removeEventListener: vi.fn(),
        dispatchEvent: vi.fn()
      }))
    })
  })

  afterEach(() => {
    vi.useRealTimers()
  })

  it('行数据变化触发的刷新绕过前端缓存但不带 force', async () => {
    const requestBatchedUsage = vi.fn()
    const account = makeOpenAIAccount(1)
    const wrapper = mountCell(account, requestBatchedUsage)
    await flushPromises()
    requestBatchedUsage.mockClear()

    await wrapper.setProps({
      account: { ...account, updated_at: '2026-03-07T10:05:00Z', extra: { ...account.extra, codex_5h_used_percent: 55 } }
    })
    await flushPromises()

    expect(requestBatchedUsage).toHaveBeenCalledTimes(1)
    expect(requestBatchedUsage.mock.calls[0][1]).toEqual({ bypassCache: true })
  })

  it('手动刷新发 force 请求', async () => {
    const requestBatchedUsage = vi.fn()
    const wrapper = mountCell(makeOpenAIAccount(1), requestBatchedUsage)
    await flushPromises()
    requestBatchedUsage.mockClear()

    await wrapper.setProps({ manualRefreshToken: 1 })
    await flushPromises()

    expect(requestBatchedUsage).toHaveBeenCalledTimes(1)
    expect(requestBatchedUsage.mock.calls[0][1]).toEqual({ force: true })
  })

  it('空闲账号：一次手动刷新后，5s 自动刷新一分钟内不再发 force 批量请求', async () => {
    const sim = createBatchSimulator(ACCOUNT_COUNT)
    const wrappers = await mountAll(sim)

    for (const wrapper of wrappers.values()) await wrapper.setProps({ manualRefreshToken: 1 })
    await vi.advanceTimersByTimeAsync(0)
    const afterManual = sim.batches.length
    expect(sim.batches.at(-1)).toEqual({ ids: expect.any(Array), force: true })

    await runAutoRefreshFor(sim, wrappers, ONE_MINUTE_MS)

    // 手动 force 回写的 updated_at 只会引发一次非 force 刷新，之后数据不再变化
    expect(countBatches(sim, afterManual)).toEqual({ force: 0, normal: 1 })
  })

  it('持续被使用的账号：5s 自动刷新每轮只发非 force 批量请求', async () => {
    const sim = createBatchSimulator(ACCOUNT_COUNT)
    const wrappers = await mountAll(sim)
    const afterMount = sim.batches.length

    await runAutoRefreshFor(sim, wrappers, ONE_MINUTE_MS, sim.markAllUsed)

    expect(countBatches(sim, afterMount)).toEqual({
      force: 0,
      normal: ONE_MINUTE_MS / AUTO_REFRESH_INTERVAL_MS
    })
  })
})
