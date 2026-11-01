// REQ-ABT-04 C2 (UI): the A/B switch `audience-ab-toggle` on AudiencePage, next to
// `audience-generate-content`: initial value from `latest_content_version.content.b`, enabled and
// disabled together with the generate button, and the `content` route query it hands over
// (`{generate: '1', ab: '1'}` on, `{generate: '1'}` off), directly or through the regenerate
// ConfirmDialog when a content version exists. The dialog may be teleported to <body>, so elements
// are queried on document.
import { afterEach, describe, expect, it } from 'vitest'
import { flushPromises, type VueWrapper } from '@vue/test-utils'
import AudiencePage from '@/pages/AudiencePage.vue'
import ConfirmDialog from '@/components/ConfirmDialog.vue'
import type { Campaign, Content } from '@/api/types'
import { mountPage } from '../helpers/pages'
import { CAMPAIGN_ID, T0, V1, V3, campaign, content, fakeApi, sel, version } from '../helpers/p4'

const C = `/campaigns/${CAMPAIGN_ID}`

let wrapper: VueWrapper | undefined
afterEach(() => {
  wrapper?.unmount()
  wrapper = undefined
  document.body.innerHTML = ''
})

/** Content version text with a B variant (REQ-ABT-01: `b` block plus `angles`). */
function contentAB(): Content {
  return {
    ...content(),
    b: {
      sms: { body: '{{first_name}}, %30 indirim bugün gece yarısı bitiyor!' },
      email: { subject: 'Son gün: %30 indirim', body: 'Merhaba {{first_name}},\n\nSon gün.' },
    },
    angles: { a: 'İndirim oranı öne çıkar', b: 'Aciliyet: indirim bugün bitiyor' },
  } as unknown as Content
}

const withB = () => version(V1, 1, contentAB())
const withoutB = () => version(V1, 1, content())

/** A campaign whose segment matched nobody. */
function noMatch(): Campaign {
  const c = campaign('audience_ready', null)
  const audience = { matched: 0, sms: 0, email: 0 }
  return { ...c, segment: { ...c.segment, audience } } as Campaign
}

const approval = {
  approval: { id: V3, content_version_id: V1, approved_at: T0 },
} as Partial<Campaign>

async function mountAudience(camp: Campaign) {
  const a = fakeApi({ [`GET ${C}`]: () => camp })
  const r = await mountPage(AudiencePage, 'audience', { api: a.client })
  wrapper = r.wrapper
  return { a, router: r.router, w: r.wrapper }
}

const q = (id: string) => document.querySelector(sel(id)) as HTMLElement | null

async function click(id: string) {
  const el = q(id)
  if (!el) throw new Error(`${id} is not in the DOM`)
  el.click()
  await flushPromises()
}

describe('AudiencePage A/B switch (REQ-ABT-04 C2)', () => {
  // REQ-ABT-04 C2
  it('loads the campaign with GET /api/campaigns/{campaign_id}', async () => {
    const { a } = await mountAudience(campaign('audience_ready', null))
    expect(a.count('GET', C)).toBeGreaterThanOrEqual(1)
  })

  // REQ-ABT-04 C2
  it.each([
    {
      name: 'no content version starts off',
      camp: () => campaign('audience_ready', null),
      want: 'false',
    },
    {
      name: 'a version without b starts off',
      camp: () => campaign('content_ready', withoutB()),
      want: 'false',
    },
    {
      name: 'a version with b starts on',
      camp: () => campaign('content_ready', withB()),
      want: 'true',
    },
  ])(
    '$name: role="switch" and aria-checked from latest_content_version.content.b',
    async ({ camp, want }) => {
      await mountAudience(camp())
      const toggle = q('audience-ab-toggle')
      expect(toggle).not.toBeNull()
      expect(toggle?.getAttribute('role')).toBe('switch')
      expect(toggle?.getAttribute('aria-checked')).toBe(want)
    },
  )

  // REQ-ABT-04 C2
  it.each([
    {
      name: 'audience_ready without a version',
      camp: () => campaign('audience_ready', null),
      from: 'false',
      to: 'true',
    },
    {
      name: 'content_ready with a version without b',
      camp: () => campaign('content_ready', withoutB()),
      from: 'false',
      to: 'true',
    },
    {
      name: 'content_ready with a version with b',
      camp: () => campaign('content_ready', withB()),
      from: 'true',
      to: 'false',
    },
  ])('$name: the switch is enabled and a click flips it', async ({ camp, from, to }) => {
    await mountAudience(camp())
    expect(q('audience-generate-content')).not.toBeNull()
    expect(q('audience-ab-toggle')?.getAttribute('aria-disabled') ?? 'false').not.toBe('true')
    expect(q('audience-ab-toggle')?.getAttribute('aria-checked')).toBe(from)
    await click('audience-ab-toggle')
    expect(q('audience-ab-toggle')?.getAttribute('aria-checked')).toBe(to)
  })

  // REQ-ABT-04 C2
  it.each([
    { name: 'draft', camp: () => campaign('draft', null), checked: 'false' },
    { name: 'audience.matched = 0', camp: noMatch, checked: 'false' },
    { name: 'scheduled', camp: () => campaign('scheduled', withB(), approval), checked: 'true' },
    { name: 'sending', camp: () => campaign('sending', withB()), checked: 'true' },
    { name: 'completed', camp: () => campaign('completed', withoutB()), checked: 'false' },
  ])(
    '$name: the switch is aria-disabled, a click keeps aria-checked and generate does not navigate',
    async ({ camp, checked }) => {
      const { router } = await mountAudience(camp())
      const toggle = q('audience-ab-toggle')
      expect(toggle).not.toBeNull()
      expect(toggle?.getAttribute('aria-disabled')).toBe('true')
      expect(toggle?.getAttribute('aria-checked')).toBe(checked)
      await click('audience-ab-toggle')
      expect(q('audience-ab-toggle')?.getAttribute('aria-checked')).toBe(checked)
      const gen = q('audience-generate-content')
      if (gen) {
        gen.click()
        await flushPromises()
      }
      expect(router.currentRoute.value.name).toBe('audience')
      expect(q('confirm-dialog')).toBeNull()
    },
  )

  // REQ-ABT-04 C2
  it.each([
    {
      name: 'no version, switch left off: straight to content with generate only',
      camp: () => campaign('audience_ready', null),
      flip: false,
      confirm: false,
      query: { generate: '1' },
    },
    {
      name: 'no version, switch turned on: straight to content with generate and ab',
      camp: () => campaign('audience_ready', null),
      flip: true,
      confirm: false,
      query: { generate: '1', ab: '1' },
    },
    {
      name: 'version without b, switch left off: confirm first, then generate only',
      camp: () => campaign('content_ready', withoutB()),
      flip: false,
      confirm: true,
      query: { generate: '1' },
    },
    {
      name: 'version without b, switch turned on: confirm first, then generate and ab',
      camp: () => campaign('content_ready', withoutB()),
      flip: true,
      confirm: true,
      query: { generate: '1', ab: '1' },
    },
    {
      name: 'version with b, switch left on: confirm first, then generate and ab',
      camp: () => campaign('content_ready', withB()),
      flip: false,
      confirm: true,
      query: { generate: '1', ab: '1' },
    },
    {
      name: 'version with b, switch turned off: confirm first, then generate only',
      camp: () => campaign('content_ready', withB()),
      flip: true,
      confirm: true,
      query: { generate: '1' },
    },
  ])('$name', async ({ camp, flip, confirm, query }) => {
    const { router, w } = await mountAudience(camp())
    if (flip) await click('audience-ab-toggle')
    await click('audience-generate-content')
    if (confirm) {
      expect(router.currentRoute.value.name).toBe('audience')
      expect(q('confirm-dialog')).not.toBeNull()
      expect(w.findComponent(ConfirmDialog).props('kind')).toBe('regenerate')
      await click('confirm-dialog-confirm')
    } else {
      expect(q('confirm-dialog')).toBeNull()
    }
    const route = router.currentRoute.value
    expect(route.name).toBe('content')
    expect(route.params.campaignId).toBe(CAMPAIGN_ID)
    expect(route.query).toEqual(query)
  })
})
