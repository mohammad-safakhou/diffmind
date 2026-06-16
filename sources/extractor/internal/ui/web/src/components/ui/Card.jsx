// Card is a bordered surface used for repo cards, file-workflow panels, etc.
export function Card({ class: cls = '', interactive = false, children, ...rest }) {
  const classes = ['ui-card']
  if (interactive) classes.push('interactive')
  if (cls) classes.push(cls)
  return <div class={classes.join(' ')} {...rest}>{children}</div>
}
