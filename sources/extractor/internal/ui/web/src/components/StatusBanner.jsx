import { runMeta } from '../lib/store.js'

export function StatusBanner() {
  const meta = runMeta.value
  if (!meta) return null

  if (meta.status === 'failed') {
    return (
      <div class="banner-strip error">
        <strong>Run failed.</strong>
        <span>{meta.error || 'See the activity log for the failing deterministic stage.'}</span>
        <ul class="banner-hints">
          <li>Check the failed stage payload in the activity drawer.</li>
          <li>Verify the repository path is readable and contains supported source/config files.</li>
          <li>Fix detector/configuration errors, then start a new deterministic run.</li>
        </ul>
      </div>
    )
  }

  if (meta.status === 'cancelled') {
    return (
      <div class="banner-strip warn">
        <strong>Run cancelled.</strong>
        <span>{meta.error || 'Partial artifacts, if any, remain on disk.'}</span>
      </div>
    )
  }

  if (meta.status === 'completed' && meta.empty) {
    return (
      <div class="banner-strip warn">
        <strong>Run completed with no entities.</strong>
        <span>No deterministic detector produced service-context objects for this repository.</span>
        <ul class="banner-hints">
          <li>Add or correct <code>diffmind-configuration.yaml</code> when the repo uses company-specific conventions.</li>
          <li>Check whether the language/framework is supported by the deterministic detector registry.</li>
        </ul>
      </div>
    )
  }

  return null
}
