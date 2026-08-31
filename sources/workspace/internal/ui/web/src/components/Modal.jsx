// Modal + ConfirmDialog: the single confirmation surface every destructive
// action funnels through before calling a delete API.

export function Modal({ title, onClose, children, wide }) {
  return (
    <div class="modal-backdrop" onClick={onClose}>
      <div class={'modal' + (wide ? ' wide' : '')} onClick={(e) => e.stopPropagation()}>
        <div class="modal-head">
          <h2>{title}</h2>
          <button class="btn ghost tiny" onClick={onClose}>✕</button>
        </div>
        <div class="modal-body">{children}</div>
      </div>
    </div>
  )
}

export function ConfirmDialog({ title, message, confirmLabel = 'Delete', onConfirm, onCancel }) {
  return (
    <div class="modal-backdrop" onClick={onCancel}>
      <div class="modal confirm" onClick={(e) => e.stopPropagation()}>
        <div class="modal-head"><h2>{title}</h2></div>
        <div class="modal-body">
          <p class="confirm-message">{message}</p>
          <div class="actions">
            <button class="btn danger" onClick={onConfirm}>{confirmLabel}</button>
            <button class="btn ghost" onClick={onCancel}>Cancel</button>
          </div>
        </div>
      </div>
    </div>
  )
}
