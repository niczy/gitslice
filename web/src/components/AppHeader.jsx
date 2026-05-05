import { useEffect, useMemo, useRef, useState } from 'react';
import { UserButton } from '@clerk/react';
import { BookOpen, Github, LibraryBig, LogIn, Search } from 'lucide-react';

import { Button } from './ui/button.jsx';

const SEARCH_RESULTS_PAGE_SIZE = 50;

function buildSearchSnippet(match) {
  const line = String(match?.line || '');
  if (!line.trim()) {
    return null;
  }
  const cleanSegment = (value) => value.replace(/\s+/g, ' ');

  const matchStart = Math.max(0, Number(match?.match_start ?? match?.matchStart ?? 0) || 0);
  const matchEnd = Math.max(matchStart, Number(match?.match_end ?? match?.matchEnd ?? 0) || 0);
  if (matchEnd <= matchStart || matchStart >= line.length) {
    return {
      before: cleanSegment(line.length > 120 ? `${line.slice(0, 117)}...` : line.trim()),
      match: '',
      after: '',
    };
  }

  const prefixRadius = 18;
  const suffixRadius = 72;
  const start = Math.max(0, matchStart - prefixRadius);
  const end = Math.min(line.length, matchEnd + suffixRadius);
  return {
    before: cleanSegment(`${start > 0 ? '...' : ''}${line.slice(start, matchStart)}`),
    match: cleanSegment(line.slice(matchStart, matchEnd)),
    after: cleanSegment(`${line.slice(matchEnd, end)}${end < line.length ? '...' : ''}`),
  };
}

export default function AppHeader({
  isAuthenticated,
  authSessionSource,
  githubUrl,
  navigate,
  onOpenRepos,
  onLogin,
  isNavActive,
  browserSearch,
}) {
  const showBrowserSearch = Boolean(browserSearch?.visible);
  const [searchDropdownOpen, setSearchDropdownOpen] = useState(false);
  const [searchResultLimit, setSearchResultLimit] = useState(SEARCH_RESULTS_PAGE_SIZE);
  const searchFormRef = useRef(null);
  const searchFiles = useMemo(() => {
    const filesByPath = new Map();
    for (const match of browserSearch?.matches || []) {
      const path = String(match?.path || '').trim();
      if (!path) {
        continue;
      }
      const lineNumber = Number(match?.line_number || match?.lineNumber || 1) || 1;
      const snippet = buildSearchSnippet(match);
      const existing = filesByPath.get(path);
      if (existing) {
        existing.matchCount += 1;
        if (!existing.snippet && snippet) {
          existing.snippet = snippet;
          existing.lineNumber = lineNumber;
        }
        continue;
      }
      filesByPath.set(path, {
        path,
        matchCount: 1,
        lineNumber,
        snippet,
      });
    }
    return Array.from(filesByPath.values()).sort((a, b) => a.path.localeCompare(b.path));
  }, [browserSearch?.matches]);
  const visibleSearchFiles = searchFiles.slice(0, searchResultLimit);
  const hiddenSearchFileCount = Math.max(0, searchFiles.length - visibleSearchFiles.length);
  const nextSearchFileCount = Math.min(SEARCH_RESULTS_PAGE_SIZE, hiddenSearchFileCount);
  const hasSearchContent = Boolean(browserSearch?.error || searchFiles.length > 0 || browserSearch?.empty || browserSearch?.loading);

  useEffect(() => {
    if (!showBrowserSearch) {
      setSearchDropdownOpen(false);
    }
  }, [showBrowserSearch]);

  useEffect(() => {
    setSearchResultLimit(SEARCH_RESULTS_PAGE_SIZE);
  }, [browserSearch?.glob, browserSearch?.query, browserSearch?.regex]);

  useEffect(() => {
    setSearchResultLimit((limit) => Math.min(Math.max(limit, SEARCH_RESULTS_PAGE_SIZE), Math.max(searchFiles.length, SEARCH_RESULTS_PAGE_SIZE)));
  }, [searchFiles.length]);

  useEffect(() => {
    if (!showBrowserSearch || !searchDropdownOpen) {
      return undefined;
    }

    const handlePointerDown = (event) => {
      if (!searchFormRef.current?.contains(event.target)) {
        setSearchDropdownOpen(false);
      }
    };
    const handleFocusIn = (event) => {
      if (!searchFormRef.current?.contains(event.target)) {
        setSearchDropdownOpen(false);
      }
    };
    const handleKeyDown = (event) => {
      if (event.key === 'Escape') {
        setSearchDropdownOpen(false);
      }
    };

    document.addEventListener('pointerdown', handlePointerDown);
    document.addEventListener('focusin', handleFocusIn);
    document.addEventListener('keydown', handleKeyDown);

    return () => {
      document.removeEventListener('pointerdown', handlePointerDown);
      document.removeEventListener('focusin', handleFocusIn);
      document.removeEventListener('keydown', handleKeyDown);
    };
  }, [searchDropdownOpen, showBrowserSearch]);

  const handleQueryChange = (value) => {
    setSearchDropdownOpen(true);
    browserSearch.onQueryChange(value);
  };

  const handleSearchSubmit = (event) => {
    setSearchDropdownOpen(true);
    browserSearch.onSubmit(event);
  };

  const handleOpenSearchResult = (path) => {
    setSearchDropdownOpen(false);
    browserSearch.onOpenResult(path);
  };

  const handleLoadMoreSearchResults = () => {
    setSearchDropdownOpen(true);
    setSearchResultLimit((limit) => Math.min(limit + SEARCH_RESULTS_PAGE_SIZE, searchFiles.length));
  };

  return (
    <header className="top-bar border-b border-border/80 bg-card/90 backdrop-blur-sm">
      <Button
        type="button"
        variant="ghost"
        className="brand"
        onClick={() => (isAuthenticated ? onOpenRepos() : navigate('landing'))}
      >
        <span className="brand-icon" aria-hidden="true"><LibraryBig size={18} /></span>
        <span className="brand-text">Git Slice</span>
      </Button>
      {showBrowserSearch && (
        <form
          ref={searchFormRef}
          className="top-search"
          data-testid="repo-search-panel"
          onSubmit={handleSearchSubmit}
        >
          <div className="top-search-field">
            <div className="top-search-main">
              <Search size={15} aria-hidden="true" />
              <input
                type="search"
                className="top-search-input"
                placeholder="Search files as you type"
                value={browserSearch.query}
                onChange={(event) => handleQueryChange(event.target.value)}
                onFocus={() => setSearchDropdownOpen(true)}
                data-testid="repo-search-query"
              />
              <Button
                type="submit"
                variant="secondary"
                size="sm"
                className="top-search-submit"
                disabled={browserSearch.loading}
                data-testid="repo-search-submit"
              >
                {browserSearch.loading ? 'Searching...' : 'Search'}
              </Button>
            </div>
            {searchDropdownOpen && hasSearchContent && (
              <div className="top-search-results">
                {browserSearch.error && <div className="panel-error">{browserSearch.error}</div>}
                {!browserSearch.error && browserSearch.loading && searchFiles.length === 0 && (
                  <div className="panel-empty">Searching files...</div>
                )}
                {!browserSearch.error && browserSearch.empty && <div className="panel-empty">No matches found for this query.</div>}
                {searchFiles.length > 0 && (
                  <ul className="repo-search-results" data-testid="repo-search-results">
                    {visibleSearchFiles.map((file) => (
                      <li key={file.path}>
                        <Button
                          type="button"
                          variant="ghost"
                          className="repo-search-result"
                          onClick={() => handleOpenSearchResult(file.path)}
                          data-testid="repo-search-result"
                        >
                          <span className="repo-search-result-main">
                            <span className="repo-search-result-path">{file.path}</span>
                            <span className="repo-search-result-count">
                              {file.matchCount} {file.matchCount === 1 ? 'match' : 'matches'}
                            </span>
                          </span>
                          {file.snippet && (
                            <span className="repo-search-result-meta">
                              <span>Line {file.lineNumber}: </span>
                              <code>
                                {file.snippet.before}
                                {file.snippet.match && <mark>{file.snippet.match}</mark>}
                                {file.snippet.after}
                              </code>
                            </span>
                          )}
                        </Button>
                      </li>
                    ))}
                    {hiddenSearchFileCount > 0 && (
                      <li className="repo-search-overflow">
                        <Button
                          type="button"
                          variant="ghost"
                          className="repo-search-load-more"
                          onClick={handleLoadMoreSearchResults}
                          data-testid="repo-search-load-more"
                        >
                          Load {nextSearchFileCount} more
                          <span>
                            {hiddenSearchFileCount} matching {hiddenSearchFileCount === 1 ? 'file' : 'files'} remaining
                          </span>
                        </Button>
                      </li>
                    )}
                  </ul>
                )}
              </div>
            )}
          </div>
          <div className="top-search-advanced">
            <input
              type="text"
              className="top-search-glob"
              placeholder="Glob"
              value={browserSearch.glob}
              onChange={(event) => browserSearch.onGlobChange(event.target.value)}
              data-testid="repo-search-glob"
            />
            <label className="top-search-regex">
              <input
                type="checkbox"
                checked={browserSearch.regex}
                onChange={(event) => browserSearch.onRegexChange(event.target.checked)}
                data-testid="repo-search-regex"
              />
              Regex
            </label>
          </div>
        </form>
      )}
      <div className="top-bar-actions">
        {isAuthenticated ? (
          <>
            {authSessionSource === 'clerk' && (
              <UserButton
                afterSignOutUrl="/"
                showName={false}
              />
            )}
          </>
        ) : (
          <>
            <Button
              type="button"
              variant={isNavActive('repos') ? 'default' : 'secondary'}
              className={`nav-link${isNavActive('repos') ? ' nav-link--active' : ''}`}
              data-testid="topbar-repo-browser"
              onClick={() => navigate('browser')}
            >
              <LibraryBig size={16} aria-hidden="true" />
              Repo Browser
            </Button>
            <Button
              type="button"
              variant={isNavActive('docs') ? 'secondary' : 'ghost'}
              className={`nav-link${isNavActive('docs') ? ' nav-link--active' : ''}`}
              data-testid="topbar-docs-link"
              onClick={() => navigate('docs')}
            >
              <BookOpen size={16} aria-hidden="true" />
              Docs
            </Button>
            <Button
              asChild
              variant="ghost"
              className="nav-link"
              data-testid="topbar-github-link"
            >
              <a href={githubUrl} target="_blank" rel="noreferrer">
                <Github size={16} aria-hidden="true" />
                GitHub
              </a>
            </Button>
            <Button
              type="button"
              variant={isNavActive('login') ? 'secondary' : 'ghost'}
              className={`nav-link${isNavActive('login') ? ' nav-link--active' : ''}`}
              data-testid="topbar-login"
              onClick={onLogin}
            >
              <LogIn size={16} aria-hidden="true" />
              Login
            </Button>
            <Button
              type="button"
              variant="default"
              className={`nav-link${isNavActive('get-started') ? ' nav-link--active' : ''}`}
              data-testid="topbar-get-started"
              onClick={() => navigate('landing')}
            >
              Get Started
            </Button>
          </>
        )}
      </div>
    </header>
  );
}
