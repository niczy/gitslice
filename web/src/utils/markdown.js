import { escapeHtml } from './highlight.js';

function getUrlBase() {
  if (typeof window !== 'undefined' && window.location?.origin) {
    return window.location.origin;
  }
  return 'https://gitslice.io';
}

function sanitizeHref(rawUrl) {
  const trimmed = (rawUrl || '').trim();
  if (!trimmed) {
    return '#';
  }

  if (trimmed.startsWith('#') || trimmed.startsWith('/')) {
    return trimmed;
  }

  try {
    const parsed = new URL(trimmed, getUrlBase());
    if (parsed.protocol === 'http:' || parsed.protocol === 'https:' || parsed.protocol === 'mailto:') {
      return parsed.toString();
    }
  } catch {
    return '#';
  }

  return '#';
}

function stripInlineMarkdown(text) {
  return String(text || '')
    .replace(/`([^`]+)`/g, '$1')
    .replace(/\*\*([^*]+)\*\*/g, '$1')
    .replace(/\*([^*]+)\*/g, '$1')
    .replace(/\[([^\]]+)\]\(([^)]+)\)/g, '$1');
}

export function slugifyMarkdownHeading(text) {
  return stripInlineMarkdown(text)
    .toLowerCase()
    .replace(/&[a-z0-9#]+;/gi, '')
    .replace(/[^a-z0-9]+/g, '-')
    .replace(/^-+|-+$/g, '') || 'section';
}

export function extractMarkdownHeadings(source) {
  const counts = new Map();
  return String(source || '')
    .replace(/\r\n/g, '\n')
    .split('\n')
    .map((line) => line.match(/^(#{1,6})\s+(.+)/))
    .filter(Boolean)
    .map((match) => {
      const baseId = slugifyMarkdownHeading(match[2]);
      const count = counts.get(baseId) || 0;
      counts.set(baseId, count + 1);
      return {
        id: count ? `${baseId}-${count + 1}` : baseId,
        level: match[1].length,
        text: stripInlineMarkdown(match[2]).trim(),
      };
    });
}

function renderInlineMarkdown(text) {
  const escaped = escapeHtml(text);

  return escaped
    .replace(/`([^`]+)`/g, '<code>$1</code>')
    .replace(/\*\*([^*]+)\*\*/g, '<strong>$1</strong>')
    .replace(/\*([^*]+)\*/g, '<em>$1</em>')
    .replace(/\[([^\]]+)\]\(([^)]+)\)/g, (_, label, href) => {
      const safeHref = escapeHtml(sanitizeHref(href));
      return `<a href="${safeHref}" target="_blank" rel="noreferrer">${label}</a>`;
    });
}

export function renderMarkdownHtml(source, options = {}) {
  if (!source) {
    return '';
  }

  const lines = source.replace(/\r\n/g, '\n').split('\n');
  const html = [];
  let inCodeBlock = false;
  let inList = false;
  let listType = '';
  const headingCounts = new Map();

  const closeList = () => {
    if (inList) {
      html.push(listType === 'ol' ? '</ol>' : '</ul>');
      inList = false;
      listType = '';
    }
  };

  for (const line of lines) {
    const fencedCodeMatch = line.match(/^```/);
    if (fencedCodeMatch) {
      closeList();
      if (!inCodeBlock) {
        inCodeBlock = true;
        html.push('<pre><code>');
      } else {
        inCodeBlock = false;
        html.push('</code></pre>');
      }
      continue;
    }

    if (inCodeBlock) {
      html.push(`${escapeHtml(line)}\n`);
      continue;
    }

    if (!line.trim()) {
      closeList();
      continue;
    }

    const headingMatch = line.match(/^(#{1,6})\s+(.+)/);
    if (headingMatch) {
      closeList();
      const level = headingMatch[1].length;
      const headingText = stripInlineMarkdown(headingMatch[2]).trim();
      const baseId = slugifyMarkdownHeading(headingMatch[2]);
      const count = headingCounts.get(baseId) || 0;
      headingCounts.set(baseId, count + 1);
      const id = count ? `${baseId}-${count + 1}` : baseId;
      const content = renderInlineMarkdown(headingMatch[2]);
      const permalink = options.headingLinks
        ? `<a class="markdown-heading-link" href="#${escapeHtml(id)}" aria-label="Link to ${escapeHtml(headingText)}">#</a>`
        : '';
      html.push(`<h${level} id="${escapeHtml(id)}">${permalink}${content}</h${level}>`);
      continue;
    }

    const blockquoteMatch = line.match(/^>\s?(.*)/);
    if (blockquoteMatch) {
      closeList();
      html.push(`<blockquote>${renderInlineMarkdown(blockquoteMatch[1])}</blockquote>`);
      continue;
    }

    const orderedListMatch = line.match(/^\d+\.\s+(.+)/);
    const unorderedListMatch = line.match(/^[-*+]\s+(.+)/);
    if (orderedListMatch || unorderedListMatch) {
      const nextType = orderedListMatch ? 'ol' : 'ul';
      const itemText = orderedListMatch ? orderedListMatch[1] : unorderedListMatch[1];
      if (!inList || listType !== nextType) {
        closeList();
        inList = true;
        listType = nextType;
        html.push(nextType === 'ol' ? '<ol>' : '<ul>');
      }
      html.push(`<li>${renderInlineMarkdown(itemText)}</li>`);
      continue;
    }

    if (/^([-*_]){3,}$/.test(line.trim())) {
      closeList();
      html.push('<hr />');
      continue;
    }

    closeList();
    html.push(`<p>${renderInlineMarkdown(line)}</p>`);
  }

  closeList();

  if (inCodeBlock) {
    html.push('</code></pre>');
  }

  return html.join('\n');
}
