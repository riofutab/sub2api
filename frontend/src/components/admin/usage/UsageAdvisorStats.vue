<template>
  <div v-if="advisors.length > 0" class="card p-4">
    <div class="mb-3 flex flex-wrap items-baseline justify-between gap-2">
      <h3 class="text-sm font-semibold text-gray-900 dark:text-white">{{ t('usage.advisor.title') }}</h3>
      <p class="text-xs text-gray-500 dark:text-dark-400">{{ t('usage.advisor.hint') }}</p>
    </div>
    <div class="overflow-x-auto">
      <table class="w-full min-w-[640px] text-sm">
        <thead>
          <tr class="border-b border-gray-200 text-left text-xs font-medium text-gray-500 dark:border-dark-700 dark:text-dark-400">
            <th class="py-2 pr-4">{{ t('usage.advisor.model') }}</th>
            <th class="py-2 pr-4 text-right">{{ t('usage.advisor.requests') }}</th>
            <th class="py-2 pr-4 text-right">{{ t('usage.advisor.calls') }}</th>
            <th class="py-2 pr-4 text-right">{{ t('usage.in') }}</th>
            <th class="py-2 pr-4 text-right">{{ t('usage.out') }}</th>
            <th class="py-2 pr-4 text-right">{{ t('usage.cacheHit') }} / {{ t('usage.cacheCreate') }}</th>
            <th class="py-2 text-right">{{ t('usage.totalCost') }}</th>
          </tr>
        </thead>
        <tbody>
          <tr
            v-for="row in advisors"
            :key="row.model"
            class="border-b border-gray-100 last:border-0 dark:border-dark-800"
          >
            <td class="py-2 pr-4 font-medium text-gray-900 dark:text-white">{{ row.model }}</td>
            <td class="py-2 pr-4 text-right tabular-nums">{{ row.requests.toLocaleString() }}</td>
            <td class="py-2 pr-4 text-right tabular-nums">{{ row.calls.toLocaleString() }}</td>
            <td class="py-2 pr-4 text-right tabular-nums">{{ formatTokens(row.input_tokens) }}</td>
            <td class="py-2 pr-4 text-right tabular-nums">{{ formatTokens(row.output_tokens) }}</td>
            <td class="py-2 pr-4 text-right tabular-nums">
              {{ formatTokens(row.cache_read_tokens) }} / {{ formatTokens(row.cache_creation_tokens) }}
            </td>
            <td class="py-2 text-right tabular-nums">
              <span class="font-medium text-green-600">${{ row.actual_cost.toFixed(4) }}</span>
              <span class="ml-1 text-xs text-gray-400">${{ row.cost.toFixed(4) }}</span>
            </td>
          </tr>
        </tbody>
      </table>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import type { AdvisorStat } from '@/types'

const props = defineProps<{
  advisors?: AdvisorStat[] | null
}>()

const { t } = useI18n()

const advisors = computed(() => props.advisors ?? [])

const formatTokens = (value: number) => {
  if (value >= 1e9) return (value / 1e9).toFixed(2) + 'B'
  if (value >= 1e6) return (value / 1e6).toFixed(2) + 'M'
  if (value >= 1e3) return (value / 1e3).toFixed(2) + 'K'
  return value.toLocaleString()
}
</script>
