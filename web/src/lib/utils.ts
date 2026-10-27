import type { ClassValue } from 'clsx'
import { clsx } from 'clsx'
import { extendTailwindMerge } from 'tailwind-merge'

// The DESIGN.md type scale (style.css @utility text-*) are font sizes, not colours;
// without this tailwind-merge would drop `text-primary-foreground` next to `text-body-sm`.
const twMerge = extendTailwindMerge({
  extend: {
    classGroups: {
      'font-size': [
        {
          text: [
            'display',
            'h1',
            'h2',
            'h3',
            'h4',
            'body-lg',
            'body',
            'body-sm',
            'caption',
            'overline',
            'numeral-xl',
            'numeral-md',
            'numeral-sm',
          ],
        },
      ],
    },
  },
})

export function cn(...inputs: ClassValue[]) {
  return twMerge(clsx(inputs))
}
