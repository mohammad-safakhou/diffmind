// Badge is a generic pill. tone: neutral|accent|success|warn|error.
export function Badge({ tone = 'neutral', class: cls = '', children }) {
  return <span class={'ui-badge ' + tone + (cls ? ' ' + cls : '')}>{children}</span>
}
