import { defineStore } from 'pinia'
import { ref } from 'vue'

/** App-wide UI flags. `connectionLost` drives ConnectionBanner (ui.md §4 network_error). */
export const useUiStore = defineStore('ui', () => {
  const connectionLost = ref(false)
  return { connectionLost }
})
