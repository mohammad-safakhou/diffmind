import { signal } from '@preact/signals'

// A tiny global toast store. Any component calls useToast().push({kind,text});
// ToastHost (mounted once in App) renders the stack and auto-dismisses.
const toasts = signal([])
let seq = 0

function push({ kind = 'success', text }) {
  const id = ++seq
  toasts.value = [...toasts.value, { id, kind, text }]
  setTimeout(() => dismiss(id), kind === 'error' ? 7000 : 4000)
}

function dismiss(id) {
  toasts.value = toasts.value.filter((t) => t.id !== id)
}

export function useToast() {
  return {
    success: (text) => push({ kind: 'success', text }),
    error: (text) => push({ kind: 'error', text }),
    push,
  }
}

export function ToastHost() {
  return (
    <div class="toast-host">
      {toasts.value.map((t) => (
        <div key={t.id} class={'toast ' + t.kind} onClick={() => dismiss(t.id)}>
          {t.text}
        </div>
      ))}
    </div>
  )
}
