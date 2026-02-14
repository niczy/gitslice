// ---------------------------------------------------------------------------
// Code highlighting and encoding utilities
// ---------------------------------------------------------------------------

export function decodeBase64(value) {
  if (!value) {
    return '';
  }
  try {
    return decodeURIComponent(escape(window.atob(value)));
  } catch (error) {
    try {
      return window.atob(value);
    } catch (innerError) {
      return value;
    }
  }
}

export function escapeHtml(value) {
  return value
    .replace(/&/g, '&amp;')
    .replace(/</g, '&lt;')
    .replace(/>/g, '&gt;')
    .replace(/"/g, '&quot;')
    .replace(/'/g, '&#39;');
}

export function highlightCode(source) {
  if (!source) {
    return '';
  }

  const tokenRegex =
    /\/\/.*|\/\*[\s\S]*?\*\/|"(?:\\.|[^"\\])*"|'(?:\\.|[^'\\])*'|`(?:\\.|[^`\\])*`|\b(?:const|let|var|function|return|if|else|for|while|class|import|from|export|async|await|try|catch|throw|new|switch|case|break|default|true|false|null)\b|\b\d+(?:\.\d+)?\b/g;
  let lastIndex = 0;
  let result = '';

  for (const match of source.matchAll(tokenRegex)) {
    const matchIndex = match.index ?? 0;
    result += escapeHtml(source.slice(lastIndex, matchIndex));
    const token = match[0];
    const className = token.startsWith('//') || token.startsWith('/*')
      ? 'token-comment'
      : token.startsWith('"') || token.startsWith("'") || token.startsWith('`')
      ? 'token-string'
      : /^\d/.test(token)
      ? 'token-number'
      : 'token-keyword';
    result += `<span class="${className}">${escapeHtml(token)}</span>`;
    lastIndex = matchIndex + token.length;
  }

  result += escapeHtml(source.slice(lastIndex));
  return result;
}
