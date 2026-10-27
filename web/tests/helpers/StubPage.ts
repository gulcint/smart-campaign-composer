// Placeholder page for routes that are not under test (docs/contracts/ui.md §1). Not a spec file.
import { defineComponent, h } from 'vue'

export default defineComponent({
  name: 'StubPage',
  render: () => h('div', { 'data-testid': 'stub-page' }),
})
