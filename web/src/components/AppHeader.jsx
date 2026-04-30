import { UserButton } from '@clerk/react';
import { BookOpen, Github, LibraryBig, LogIn } from 'lucide-react';

import { Button } from './ui/button.jsx';

export default function AppHeader({
  isAuthenticated,
  authSessionSource,
  githubUrl,
  navigate,
  onOpenRepos,
  onLogin,
  isNavActive,
}) {
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
