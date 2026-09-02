/**
 * Enter while an input method editor is composing belongs to the IME — it commits the candidate
 * (Japanese, Chinese, Korean, and any keyboard with dead keys or autocorrect popups). Sending on it
 * cuts the word in half and posts it. `keyCode` 229 is the older tell for the same thing and is
 * still what some Android IMEs report.
 */
export function composing(e: { nativeEvent: { isComposing?: boolean }; keyCode: number }): boolean {
  return e.nativeEvent.isComposing === true || e.keyCode === 229;
}
