// Field is a labelled form control. Pass an <input>/<select>/<textarea> (or any
// control) as children, or use the `value`/`onInput` convenience for a text input.
export function Field({ label, hint, children, value, onInput, placeholder, type = 'text' }) {
  return (
    <label class="ui-field">
      <span class="ui-field-label">{label}</span>
      {children || (
        <input type={type} value={value} placeholder={placeholder} onInput={onInput} />
      )}
      {hint && <span class="ui-field-hint">{hint}</span>}
    </label>
  )
}
