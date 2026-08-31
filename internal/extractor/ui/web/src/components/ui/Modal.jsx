import { Button } from './Button.jsx'

// Modal is the shared dialog frame. size: normal|wide. Extracted from the
// inline Modal/EditorModal definitions so every dialog looks the same.
export function Modal({ title, onClose, size = 'normal', class: cls = '', children, footer }) {
  const classes = ['modal']
  if (size === 'wide') classes.push('wide')
  if (cls) classes.push(cls)
  return (
    <div class="modal-backdrop" onClick={onClose}>
      <div class={classes.join(' ')} onClick={(e) => e.stopPropagation()}>
        <div class="modal-head">
          <h2>{title}</h2>
          {onClose && <Button variant="secondary" size="tiny" onClick={onClose}>✕</Button>}
        </div>
        <div class="modal-body">{children}</div>
        {footer && <div class="modal-foot">{footer}</div>}
      </div>
    </div>
  )
}

export function ConfirmDialog({ title, message, confirmLabel = 'Confirm', danger = true, onConfirm, onCancel }) {
  return (
    <Modal title={title} onClose={onCancel} class="confirm">
      <p class="confirm-message">{message}</p>
      <div class="actions">
        <Button variant={danger ? 'danger' : 'primary'} onClick={onConfirm}>{confirmLabel}</Button>
        <Button variant="secondary" onClick={onCancel}>Cancel</Button>
      </div>
    </Modal>
  )
}
