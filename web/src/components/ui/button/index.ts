import type { VariantProps } from 'class-variance-authority'
import { cva } from 'class-variance-authority'

export { default as Button } from './Button.vue'

// Variants follow DESIGN.md §7 (Button row) instead of the shadcn defaults.
export const buttonVariants = cva(
  [
    'inline-flex shrink-0 items-center justify-center gap-2 whitespace-nowrap rounded-pill font-semibold',
    'transition-[background,box-shadow,transform,color] duration-[var(--dur-fast)] ease-standard',
    'focus-visible:shadow-focus active:scale-[.98]',
    '[&_svg]:pointer-events-none [&_svg]:shrink-0 [&_svg:not([class*=size-])]:size-4',
    'aria-disabled:cursor-not-allowed aria-disabled:opacity-45 aria-disabled:shadow-none aria-disabled:active:scale-100',
    'data-[loading=true]:cursor-progress',
  ],
  {
    variants: {
      variant: {
        primary:
          'bg-gradient-cta text-primary-foreground shadow-cta hover:bg-gradient-cta-hover hover:shadow-cta-hover aria-disabled:hover:bg-gradient-cta',
        secondary:
          'border border-brand-line bg-secondary text-secondary-foreground hover:border-brand-icon',
        ghost: 'bg-transparent text-accent-text hover:bg-accent hover:text-accent-hover',
        'danger-ghost': 'bg-transparent text-error hover:bg-error-bg',
      },
      size: {
        sm: 'h-8 px-4 text-body-sm',
        md: 'h-10 px-5 text-body-sm',
        lg: 'h-12 px-6 text-body',
      },
    },
    defaultVariants: {
      variant: 'primary',
      size: 'md',
    },
  },
)
export type ButtonVariants = VariantProps<typeof buttonVariants>
