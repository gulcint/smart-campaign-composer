// REQ-CNT-03 (UI): VariableChip (docs/contracts/ui.md §3.12).
import { describe, expect, it } from 'vitest'
import { mount } from '@vue/test-utils'
import VariableChip from '@/components/VariableChip.vue'
import { sel } from '../helpers/p4'

describe('VariableChip (REQ-CNT-03)', () => {
  it.each(['insert', 'inline', 'invalid'] as const)(
    'variant %s shows {{name}} and data-variant',
    (variant) => {
      const w = mount(VariableChip, { props: { name: 'first_name', variant } })
      const chip = w.get(sel('variable-chip-first_name'))
      expect(chip.attributes('data-variant')).toBe(variant)
      expect(chip.text()).toContain('{{first_name}}')
    },
  )

  it('an enabled insert chip emits insert(name)', async () => {
    const w = mount(VariableChip, { props: { name: 'city', variant: 'insert' } })
    await w.get(sel('variable-chip-city')).trigger('click')
    expect(w.emitted('insert')).toEqual([['city']])
  })

  const silent: {
    name: string
    props: { name: string; variant: 'insert' | 'inline' | 'invalid'; disabled?: boolean }
  }[] = [
    { name: 'disabled insert', props: { name: 'city', variant: 'insert', disabled: true } },
    { name: 'invalid', props: { name: 'password', variant: 'invalid' } },
    { name: 'inline', props: { name: 'city', variant: 'inline' } },
  ]
  it.each(silent)('$name does not emit insert', async ({ props }) => {
    const w = mount(VariableChip, { props })
    await w.get(sel(`variable-chip-${props.name}`)).trigger('click')
    expect(w.emitted('insert')).toBeUndefined()
  })

  it('a name with markup stays text (REQ-UI-01)', () => {
    const w = mount(VariableChip, { props: { name: '<b>x</b>', variant: 'invalid' } })
    expect(w.element.querySelector('b')).toBeNull()
  })
})
