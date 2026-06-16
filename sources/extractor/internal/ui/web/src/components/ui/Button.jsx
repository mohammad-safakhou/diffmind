// Button wraps the existing .btn classes so views stop hand-writing class
// strings. variant: primary|secondary|danger; size: normal|tiny.
export function Button({ variant = 'primary', size = 'normal', class: cls = '', children, ...rest }) {
  const classes = ['btn']
  if (variant === 'secondary') classes.push('secondary')
  if (variant === 'danger') classes.push('danger')
  if (size === 'tiny') classes.push('tiny')
  if (cls) classes.push(cls)
  return <button class={classes.join(' ')} {...rest}>{children}</button>
}
