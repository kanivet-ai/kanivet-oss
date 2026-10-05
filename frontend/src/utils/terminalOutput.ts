// Returns what to write to xterm for a terminal output message. Base64 output
// is raw bytes that were not valid UTF-8 on their own. Written as bytes,
// xterm's UTF-8 decoder renders them; the string atob returns would show every
// byte, including each byte of a multibyte character, as a Latin-1 character.
export function terminalOutputData(
  data: string,
  encoding?: string,
): string | Uint8Array {
  if (encoding !== 'base64') {
    return data;
  }
  const binary = atob(data);
  const bytes = new Uint8Array(binary.length);
  for (let i = 0; i < binary.length; i++) {
    bytes[i] = binary.charCodeAt(i);
  }
  return bytes;
}
