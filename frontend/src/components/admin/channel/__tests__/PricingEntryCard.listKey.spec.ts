import { readFileSync } from 'node:fs'
import { resolve } from 'node:path'
import { mount, shallowMount } from '@vue/test-utils'
import { describe, expect, it, vi } from 'vitest'
import { defineComponent, h, ref } from 'vue'
import PricingEntryCard from '../PricingEntryCard.vue'
import { createPricingUiKey, type PricingFormEntry } from '../types'

vi.mock('@/api/admin/channels', () => ({
  default: { getModelDefaultPricing: vi.fn() },
}))

vi.mock('vue-i18n', async importOriginal => ({
  ...await importOriginal<typeof import('vue-i18n')>(),
  useI18n: () => ({ t: (key: string) => key }),
}))

function createEntry(models: string[]): PricingFormEntry {
  return {
    uiKey: createPricingUiKey(),
    models,
    billing_mode: 'token',
    input_price: null,
    output_price: null,
    cache_write_price: null,
    cache_read_price: null,
    image_input_price: null,
    image_output_price: null,
    per_request_price: null,
    intervals: [],
    time_pricing: { timezone: 'Asia/Shanghai', weekdays_only: false, periods: [] },
  }
}

const PricingList = defineComponent({
  props: { initial: { type: Array as () => PricingFormEntry[], required: true } },
  setup(props) {
    const entries = ref<PricingFormEntry[]>([...props.initial])
    return () => h('div', entries.value.map((entry, idx) => h(PricingEntryCard, {
      key: entry.uiKey,
      entry,
      onUpdate: (updated: PricingFormEntry) => entries.value.splice(idx, 1, updated),
      onRemove: () => entries.value.splice(idx, 1),
    })))
  },
})

const isCollapsed = (card: ReturnType<typeof mount>) =>
  card.find('.collapsible-content--collapsed').exists()

describe('PricingEntryCard list keys', () => {
  it('generates a distinct ui key for every entry', () => {
    const keys = new Set(Array.from({ length: 50 }, () => createPricingUiKey()))
    expect(keys.size).toBe(50)
  })

  it('keeps the ui key when the card emits an update', () => {
    const entry = createEntry([])
    const wrapper = shallowMount(PricingEntryCard, { props: { entry } })
    wrapper.findComponent({ name: 'ModelTagInput' }).vm.$emit('update:models', ['claude-opus-4-8'])
    const emitted = wrapper.emitted('update')?.at(-1)?.[0] as PricingFormEntry
    expect(emitted.uiKey).toBe(entry.uiKey)
  })

  it('keeps the remaining card expanded after removing the card before it', async () => {
    const wrapper = mount(PricingList, {
      props: { initial: [createEntry(['claude-opus-4-8']), createEntry([])] },
      global: { stubs: { ModelTagInput: true, IntervalRow: true, TimePricingSection: true } },
    })
    const cards = wrapper.findAllComponents(PricingEntryCard)
    expect(isCollapsed(cards[0])).toBe(true)
    expect(isCollapsed(cards[1])).toBe(false)

    cards[0].vm.$emit('remove')
    await wrapper.vm.$nextTick()

    const remaining = wrapper.findAllComponents(PricingEntryCard)
    expect(remaining).toHaveLength(1)
    expect(isCollapsed(remaining[0])).toBe(false)
  })

  it('keys every pricing card list in the admin views by ui key', () => {
    for (const file of ['src/views/admin/ChannelsView.vue', 'src/views/admin/GroupsView.vue']) {
      const source = readFileSync(resolve(file), 'utf8')
      const cards = source.match(/<PricingEntryCard[\s\S]*?\/>/g) ?? []
      expect(cards.length).toBeGreaterThan(0)
      for (const card of cards) expect(card).toContain(':key="entry.uiKey"')
    }
  })
})
