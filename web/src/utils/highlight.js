// ---------------------------------------------------------------------------
// Code highlighting, line numbers, and code folding utilities
// ---------------------------------------------------------------------------

import { decodeBase64UTF8 } from '../../shared/runtime.js';

export function decodeBase64(value) {
  if (!value) {
    return '';
  }
  try {
    return decodeBase64UTF8(value);
  } catch (error) {
    return value;
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
  return highlightCodeLines(source).html;
}

function tokenizeLine(line) {
  if (!line) return escapeHtml(line || '');
  const tokenRegex =
    /\/\/.*|\/\*[\s\S]*?\*\/|"(?:\\.|[^"\\])*"|'(?:\\.|[^'\\])*'|`(?:\\.|[^`\\])*`|\b(?:const|let|var|function|return|if|else|for|while|class|import|from|export|async|await|try|catch|throw|new|switch|case|break|default|true|false|null)\b|\b\d+(?:\.\d+)?\b/g;
  let lastIndex = 0;
  let result = '';
  for (const match of line.matchAll(tokenRegex)) {
    const matchIndex = match.index ?? 0;
    result += escapeHtml(line.slice(lastIndex, matchIndex));
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
  result += escapeHtml(line.slice(lastIndex));
  return result;
}

function isFoldStart(line, nextLine) {
  const trimmed = line.trimEnd();
  if (!trimmed) return false;
  if (trimmed.endsWith('{')) return true;
  if (/^\s*(?:function|if|for|while|switch|class|struct|interface|enum|impl|trait|mod|fn|def|else|elif|except|finally|try|with|match|case)\b/.test(trimmed)) {
    if (!trimmed.endsWith('{') && nextLine && /^\s*\{/.test(nextLine)) return true;
  }
  return false;
}

function isFoldEnd(line) {
  const trimmed = line.trimStart();
  return /^\}/.test(trimmed);
}

function isFoldBlank(line) {
  return /^\s*$/.test(line);
}

function indentLevel(line) {
  const match = line.match(/^(\s*)/);
  return match ? match[1].length : 0;
}

export function highlightCodeLines(source) {
  const lines = source ? source.split('\n') : [];
  const lineData = [];
  const folds = [];
  const foldStack = [];

  for (let i = 0; i < lines.length; i++) {
    const line = lines[i];
    const nextLine = i + 1 < lines.length ? lines[i + 1] : '';
    const num = i + 1;
    const html = tokenizeLine(line);

    lineData.push({ num, line, html, foldStart: false, foldEnd: false, foldDepth: 0 });

    if (isFoldStart(line, nextLine)) {
      foldStack.push({ startLine: num, indent: indentLevel(line) });
    }

    if (isFoldEnd(line) && foldStack.length > 0) {
      while (foldStack.length > 0) {
        const fold = foldStack.pop();
        if (fold.indent <= indentLevel(line) || foldStack.length === 0) {
          folds.push({ start: fold.startLine, end: num });
          break;
        }
      }
    }
  }

  // Close remaining folds at EOF
  while (foldStack.length > 0) {
    const fold = foldStack.pop();
    folds.push({ start: fold.startLine, end: lines.length });
  }

  // Build fold range map
  const foldInRange = new Map();
  for (const f of folds) {
    for (let ln = f.start; ln <= f.end; ln++) {
      if (!foldInRange.has(ln)) foldInRange.set(ln, []);
      foldInRange.get(ln).push(f);
    }
  }

  // Mark fold boundaries on line data
  for (const f of folds) {
    if (f.start <= lineData.length) {
      lineData[f.start - 1].foldStart = true;
    }
    if (f.end <= lineData.length) {
      lineData[f.end - 1].foldEnd = true;
    }
  }

  // Build HTML table rows
  let html = '';
  for (let i = 0; i < lineData.length; i++) {
    const ld = lineData[i];
    const foldClasses = [];
    if (ld.foldStart) foldClasses.push('fold-start');
    if (ld.foldEnd) foldClasses.push('fold-end');
    const foldRanges = foldInRange.get(ld.num) || [];

    // Compute fold depth for indentation guides
    let minFoldStart = Infinity;
    for (const f of foldRanges) {
      if (f.start < minFoldStart) minFoldStart = f.start;
    }

    const lineClass = foldClasses.join(' ');
    const dataAttrs = foldRanges.length > 0
      ? ` data-fold-range="${foldRanges.map(f => `${f.start}-${f.end}`).join(' ')}"`
      : '';

    const foldToggle = ld.foldStart
      ? `<button class="fold-toggle" data-fold-line="${ld.num}" aria-label="Toggle fold">▼</button>`
      : '';

    html += `<tr class="code-line ${lineClass}" data-line="${ld.num}"${dataAttrs}>`;
    html += `<td class="line-number" data-line-num="${ld.num}">${foldToggle}${ld.num}</td>`;
    html += `<td class="line-content">${ld.html || ' '}</td>`;
    html += '</tr>';
  }

  return { html, lineCount: lines.length, folds };
}
