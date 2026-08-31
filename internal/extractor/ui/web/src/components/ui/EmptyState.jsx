// EmptyState is the shared "nothing here yet" panel.
export function EmptyState({ title, hint, action }) {
  return (
    <div class="ui-empty">
      <div class="ui-empty-title">{title}</div>
      {hint && <div class="ui-empty-hint">{hint}</div>}
      {action && <div class="ui-empty-action">{action}</div>}
    </div>
  )
}
