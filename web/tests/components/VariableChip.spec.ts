// REQ-UI-01: VariableChip keeps a name with markup as text.
import { describe, expect, it } from 'vitest'
import { mount } from '@vue/test-utils'
import VariableChip from '@/components/VariableChip.vue'

describe('VariableChip (REQ-UI-01)', () => {
  it('a name with markup stays text (REQ-UI-01)', () => {
    const w = mount(VariableChip, { props: { name: '<b>x</b>', variant: 'invalid' } })
    expect(w.element.querySelector('b')).toBeNull()
  })
})
