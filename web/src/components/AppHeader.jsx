import { UserButton } from '@clerk/react';
import { BookOpen, Github, LibraryBig, LogIn, Search } from 'lucide-react';

import { Button } from './ui/button.jsx';

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
        <form className="top-search" data-testid="repo-search-panel" onSubmit={browserSearch.onSubmit}>
          <div className="top-search-main">
            <Search size={15} aria-hidden="true" />
            <input
              type="search"
              className="top-search-input"
              placeholder="Search files"
              value={browserSearch.query}
              onChange={(event) => browserSearch.onQueryChange(event.target.value)}
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
          {(browserSearch.error || browserSearch.matches.length > 0 || browserSearch.empty) && (
            <div className="top-search-results">
              {browserSearch.error && <div className="panel-error">{browserSearch.error}</div>}
              {!browserSearch.error && browserSearch.empty && <div className="panel-empty">No matches found for this query.</div>}
              {browserSearch.matches.length > 0 && (
                <ul className="repo-search-results" data-testid="repo-search-results">
                  {browserSearch.matches.map((match, index) => (
                    <li key={`${match.path}-${match.line_number}-${index}`}>
                      <Button
                        type="button"
                        variant="ghost"
                        className="repo-search-result"
                        onClick={() => browserSearch.onOpenResult(match.path)}
                        data-testid="repo-search-result"
                      >
                        <span className="repo-search-result-path">{match.path}</span>
                        <span className="repo-search-result-meta">Line {match.line_number || 1}</span>
                        <code className="repo-search-result-line">{match.line}</code>
                      </Button>
                    </li>
                  ))}
                </ul>
              )}
            </div>
          )}
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
