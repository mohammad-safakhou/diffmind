import { useEffect, useState } from 'preact/hooks'
import { fsList } from '../lib/api.js'
import { Modal, Button } from './ui/index.js'

// FsPicker is a server-side folder browser for choosing a diffmind.yaml without
// pasting an absolute path. Picking a .yaml file calls onPick(absolutePath).
export function FsPicker({ onPick, onClose, initialPath = '' }) {
  const [data, setData] = useState(null)
  const [error, setError] = useState('')

  const load = (path) => {
    fsList(path)
      .then((d) => { setData(d); setError('') })
      .catch((e) => setError(e.message || String(e)))
  }

  useEffect(() => { load(initialPath) }, [])

  const join = (dir, name) => (dir.endsWith('/') ? dir + name : dir + '/' + name)

  return (
    <Modal title="Choose discovery file" onClose={onClose} size="wide">
      {error && <div class="banner error">{error}</div>}
      {!data ? (
        <div class="catalog-loading">Loading…</div>
      ) : (
        <div class="fs-picker">
          <div class="fs-path">
            {data.parent && <Button variant="secondary" size="tiny" onClick={() => load(data.parent)}>↑ Up</Button>}
            <code class="fs-current">{data.path}</code>
          </div>

          {Array.isArray(data.suggestions) && data.suggestions.length > 0 && (
            <div class="fs-suggestions">
              <span class="fs-suggest-label">Known repositories</span>
              {data.suggestions.map((s) => (
                <button key={s} class="fs-suggest" onClick={() => load(s)}>{s}</button>
              ))}
            </div>
          )}

          <div class="fs-listing">
            {data.dirs.map((d) => (
              <button key={'d-' + d} class="fs-entry dir" onClick={() => load(join(data.path, d))}>
                <span class="fs-icon">📁</span>{d}
              </button>
            ))}
            {data.files.map((f) => (
              <button key={'f-' + f} class="fs-entry file" onClick={() => onPick(join(data.path, f))}>
                <span class="fs-icon">📄</span>{f}
              </button>
            ))}
            {data.dirs.length === 0 && data.files.length === 0 && (
              <div class="fs-empty">No folders or YAML files here.</div>
            )}
          </div>

          <div class="fs-foot">
            <span class="fs-hint">Pick a <code>.yaml</code> file, or browse to its folder.</span>
            <Button variant="secondary" size="tiny" onClick={() => onPick(join(data.path, 'diffmind.yaml'))}>
              Use {data.path}/diffmind.yaml
            </Button>
          </div>
        </div>
      )}
    </Modal>
  )
}
