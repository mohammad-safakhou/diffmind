import { useEffect, useState } from 'preact/hooks'
import { getCapabilities } from './api.js'

export const canCreateProject = (session) => session?.role === 'admin' || (session?.project_access === 'legacy' && session?.role === 'editor')
export function membersFromRows(rows) {
  const members = Object.create(null)
  for (const { subject, role } of rows) {
    if (!subject || subject !== subject.trim() || new TextEncoder().encode(subject).length > 256 || /[\u0000-\u001f\u007f-\u009f]/.test(subject)) throw new Error('Enter exact nonempty subjects without control characters (maximum 256 bytes).')
    if (!['viewer', 'editor'].includes(role)) throw new Error('Choose viewer or editor.')
    if (Object.hasOwn(members, subject)) throw new Error(`Duplicate subject: ${subject}`)
    members[subject] = role
  }
  if (rows.length > 1000) throw new Error('At most 1,000 members are supported.')
  return members
}

export function useProjectCapabilities(pid) {
  const [state, setState] = useState({ data: null, error: '' })
  useEffect(() => {
    let alive = true, timer
    const refresh = async () => {
      try { const data = await getCapabilities(pid); if (alive) setState({ data, error: '' }) }
      catch (e) { if (alive) setState({ data: null, error: e.message }) }
      finally { if (alive) timer = setTimeout(refresh, 3000) }
    }
    setState({ data: null, error: '' }); refresh()
    return () => { alive = false; clearTimeout(timer) }
  }, [pid])
  return state
}
