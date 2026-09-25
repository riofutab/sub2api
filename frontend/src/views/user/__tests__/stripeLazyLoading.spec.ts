import { readFileSync } from 'node:fs'
import { resolve } from 'node:path'
import { describe, expect, it } from 'vitest'

const frontendRoot = resolve(__dirname, '../../../..')
const stripeConsumers = [
  'src/views/user/StripePaymentView.vue',
  'src/views/user/StripePopupView.vue',
  'src/components/payment/StripePaymentInline.vue',
]

function readFrontendFile(path: string): string {
  return readFileSync(resolve(frontendRoot, path), 'utf8')
}

describe('Stripe lazy-loading contract', () => {
  it.each(stripeConsumers)('%s uses the side-effect-free Stripe loader', (path) => {
    const source = readFrontendFile(path)

    expect(source).toContain("await import('@stripe/stripe-js/pure')")
    expect(source).not.toMatch(/await import\(['"]@stripe\/stripe-js['"]\)/)
  })

  it('has no catch-all vendor chunk that would pull Stripe into shared dependencies', () => {
    const viteConfig = readFrontendFile('vite.config.ts')

    // 未被手动规则命中的第三方库交给 Rollup 按引用关系分包，Stripe 只会出现在动态 import 的异步 chunk 中
    expect(viteConfig).not.toContain("return 'vendor-misc'")
    expect(viteConfig).not.toContain('/@stripe/')
  })
})
