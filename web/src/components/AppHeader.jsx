import { Button } from './ui/button.jsx';

export default function AppHeader({
  isAuthenticated,
  username,
  githubUrl,
  navigate,
  onLogout,
  isNavActive,
}) {
  return (
    <header className="top-bar border-b border-border/80 bg-card/90 backdrop-blur-sm">
      <Button type="button" variant="ghost" className="brand" onClick={() => navigate('landing')}>
        <span className="brand-icon">◆</span>
        <span className="brand-text">Git Slice</span>
      </Button>
      <div className="top-bar-actions">
        {isAuthenticated ? (
          <>
            <Button
              type="button"
              variant={isNavActive('projects') ? 'secondary' : 'ghost'}
              className={`nav-link${isNavActive('projects') ? ' nav-link--active' : ''}`}
              data-testid="topbar-projects"
              onClick={() => navigate('projects')}
            >
              Projects
            </Button>
            <Button
              type="button"
              variant={isNavActive('repos') ? 'secondary' : 'ghost'}
              className={`nav-link${isNavActive('repos') ? ' nav-link--active' : ''}`}
              data-testid="topbar-repos"
              onClick={() => navigate('browser')}
            >
              Repos
            </Button>
            <Button
              type="button"
              variant={isNavActive('settings') ? 'secondary' : 'ghost'}
              className={`nav-link${isNavActive('settings') ? ' nav-link--active' : ''}`}
              data-testid="topbar-settings"
              onClick={() => navigate('settings')}
            >
              Settings
            </Button>
            <Button
              type="button"
              variant="ghost"
              className="nav-link"
              data-testid="topbar-profile"
              onClick={() => navigate('profile')}
              title="Profile"
            >
              {username}
            </Button>
            <Button
              type="button"
              variant="ghost"
              className="nav-link"
              data-testid="topbar-logout"
              onClick={onLogout}
            >
              Logout
            </Button>
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
              Repo Browser
            </Button>
            <Button
              type="button"
              variant={isNavActive('login') ? 'secondary' : 'ghost'}
              className={`nav-link${isNavActive('login') ? ' nav-link--active' : ''}`}
              data-testid="topbar-login"
              onClick={() => navigate('login')}
            >
              Login
            </Button>
            <Button asChild variant="ghost" className="nav-link" data-testid="topbar-docs-link">
              <a href="https://github.com/agenttools-dev/gitslice#readme" target="_blank" rel="noreferrer">
                Docs
              </a>
            </Button>
            <Button asChild variant="ghost" className="nav-link" data-testid="topbar-github-link">
              <a href={githubUrl} target="_blank" rel="noreferrer">
                GitHub
              </a>
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
