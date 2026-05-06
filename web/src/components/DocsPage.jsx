import { useCallback, useMemo } from 'react';

import { extractMarkdownHeadings, renderMarkdownHtml } from '../utils/markdown.js';
import { Badge } from './ui/badge.jsx';
import { Button } from './ui/button.jsx';

const FALLBACK_TITLE = 'Git Slice docs';
const FALLBACK_LEDE = 'Agent instructions for working with Git Slice.';

function splitDocsMarkdown(markdown) {
  const normalized = String(markdown || '').replace(/\r\n/g, '\n').trim();
  if (!normalized) {
    return { title: FALLBACK_TITLE, lede: FALLBACK_LEDE, body: '' };
  }

  const lines = normalized.split('\n');
  let index = 0;
  let title = FALLBACK_TITLE;
  const titleMatch = lines[index]?.match(/^#\s+(.+)/);
  if (titleMatch) {
    title = titleMatch[1].trim();
    index += 1;
  }

  while (index < lines.length && !lines[index].trim()) {
    index += 1;
  }

  const ledeLines = [];
  while (index < lines.length) {
    const line = lines[index];
    if (!line.trim()) {
      break;
    }
    if (/^(#{1,6})\s+/.test(line) || /^```/.test(line) || /^[-*+]\s+/.test(line) || /^\d+\.\s+/.test(line)) {
      break;
    }
    ledeLines.push(line);
    index += 1;
  }

  while (index < lines.length && !lines[index].trim()) {
    index += 1;
  }

  return {
    title,
    lede: ledeLines.join(' ').trim() || FALLBACK_LEDE,
    body: lines.slice(index).join('\n').trim(),
  };
}

export default function DocsPage({ markdown = '', onBrowseRepo }) {
  const docs = useMemo(() => splitDocsMarkdown(markdown), [markdown]);
  const navItems = useMemo(
    () => extractMarkdownHeadings(docs.body).filter((heading) => heading.level === 2),
    [docs.body],
  );
  const docsHtml = useMemo(() => renderMarkdownHtml(docs.body, { headingLinks: true }), [docs.body]);
  const handleNavClick = useCallback((event, id) => {
    if (
      event.defaultPrevented ||
      event.button !== 0 ||
      event.metaKey ||
      event.altKey ||
      event.ctrlKey ||
      event.shiftKey
    ) {
      return;
    }

    const target = document.getElementById(id);
    if (!target) {
      return;
    }
    event.preventDefault();
    window.history.pushState(null, '', `#${id}`);
    target.scrollIntoView({ block: 'start' });
  }, []);

  return (
    <div className="docs-page">
      <section className="docs-hero">
        <div className="docs-hero-copy">
          <Badge variant="secondary" className="eyebrow">Git Slice docs</Badge>
          <h1>{docs.title}</h1>
          <p className="lede">{docs.lede}</p>
          <div className="cta-row flex flex-wrap gap-3">
            <Button type="button" onClick={onBrowseRepo}>
              Open slices
            </Button>
            <Button asChild variant="outline">
              <a href="/docs.md">Open docs.md</a>
            </Button>
          </div>
        </div>
        <div className="docs-hero-card">
          <div className="docs-hero-card-head">
            <Badge variant="outline" className="w-fit">Source of truth</Badge>
            <p>Rendered from docs.md at runtime</p>
          </div>
          <pre className="code-block">
            <code>{`curl -fsSL https://raw.githubusercontent.com/niczy/gitslice/main/install-gs.sh | sh
gs auth keygen --out ~/.config/gitslice/agent_ed25519
gs auth login --key ~/.config/gitslice/agent_ed25519`}</code>
          </pre>
        </div>
      </section>

      <div className="docs-layout">
        <aside className="docs-nav-shell">
          <div className="docs-nav-card">
            <p className="docs-nav-kicker">Navigate</p>
            <nav className="docs-nav" aria-label="Documentation navigation">
              {navItems.map((item) => (
                <a
                  key={item.id}
                  className="docs-nav-link"
                  href={`#${item.id}`}
                  onClick={(event) => handleNavClick(event, item.id)}
                >
                  {item.text}
                </a>
              ))}
            </nav>
          </div>
        </aside>

        <article
          className="docs-content docs-markdown"
          dangerouslySetInnerHTML={{ __html: docsHtml || '<p>Documentation is empty.</p>' }}
        />
      </div>
    </div>
  );
}
