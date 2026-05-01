import { useCallback, useEffect, useRef, useState } from 'react';
import { useQueryClient } from '@tanstack/react-query';

import { buildLegacyRedirectPath, buildPath } from './utils/routing.js';
import { apiBaseUrl, currentUsername, searchWorkspaceFiles } from './utils/api.js';
import { signInWithAccount, signOutAccount, startOAuthSignIn, startOAuthSignOut } from './auth.js';
import { useWebSession } from './hooks/useWebSession.js';
import { useSlicesQuery } from './hooks/useSlices.js';

import OverviewPage from './components/OverviewPage.jsx';
import DocsPage from './components/DocsPage.jsx';
import AppHeader from './components/AppHeader.jsx';
import AppFooter from './components/AppFooter.jsx';
import AdminPage from './components/AdminPage.jsx';
import LoginPage from './components/LoginPage.jsx';
import ProfilePage from './components/ProfilePage.jsx';
import RepoBrowser from './components/RepoBrowser.jsx';
import ProjectsPage from './components/ProjectsPage.jsx';
import SettingsPage from './components/SettingsPage.jsx';
import NotFoundPage from './components/NotFoundPage.jsx';
import CommitDiffPage from './components/CommitDiffPage.jsx';
import ChangesetDiffPage from './components/ChangesetDiffPage.jsx';
import RouteAccessState from './components/RouteAccessState.jsx';
import { trackRouteEvent } from './utils/analytics.js';

function getPreferredSliceId(slices, username) {
  const trimmedUsername = String(username || '').trim();
  if (trimmedUsername) {
    const homeSliceId = getHomeSliceId(trimmedUsername);
    const homeSlice = slices.find((slice) => slice.slice_id === homeSliceId);
    if (homeSlice) {
      return homeSlice.slice_id;
    }
  }

  const root = slices.find((slice) => slice.is_root);
  return root ? root.slice_id : slices[0]?.slice_id || '';
}

function getHomeSliceId(username) {
  const trimmedUsername = String(username || '').trim().toLowerCase();
  return trimmedUsername ? `home.${trimmedUsername}` : '';
}

function App({
  initialRoute,
  initialAuthConfig = { authProvider: 'local', allowDevLogin: true },
  initialSession = null,
  initialSessionError = '',
  routerNavigate,
}) {
  const queryClient = useQueryClient();
  const initialUsername = initialSession?.user?.username || currentUsername();
  const initialPage = initialRoute.page === 'landing' && initialUsername ? 'browser' : initialRoute.page;
  const [activePage, setActivePage] = useState(() => initialPage);
  const [diffCommitHash, setDiffCommitHash] = useState(() => initialRoute.commitHash);
  const [diffChangesetId, setDiffChangesetId] = useState(() => initialRoute.changesetId);
  const [unknownRoute, setUnknownRoute] = useState(() => initialRoute.unknownPath || '');
  const [returnToPage, setReturnToPage] = useState('browser');
  const [returnToCommitHash, setReturnToCommitHash] = useState('');
  const [returnToChangesetId, setReturnToChangesetId] = useState('');
  const [username, setUsername] = useState(() => initialUsername);
  const [authSessionSource, setAuthSessionSource] = useState(() => initialSession?.source || '');
  const [browserMounted, setBrowserMounted] = useState(() => initialPage === 'browser');
  const [currentSliceId, setCurrentSliceId] = useState(() => getHomeSliceId(initialUsername));
  const [historyRefreshToken, setHistoryRefreshToken] = useState(0);
  const [browserSearchQuery, setBrowserSearchQuery] = useState('');
  const [browserSearchGlob, setBrowserSearchGlob] = useState('');
  const [browserSearchRegex, setBrowserSearchRegex] = useState(false);
  const [browserSearchMatches, setBrowserSearchMatches] = useState([]);
  const [browserSearchLoading, setBrowserSearchLoading] = useState(false);
  const [browserSearchError, setBrowserSearchError] = useState('');
  const [browserSearchHasSearched, setBrowserSearchHasSearched] = useState(false);
  const [browserOpenFileRequest, setBrowserOpenFileRequest] = useState(null);
  const previousUsernameRef = useRef(username);
  const hasExplicitSliceSelectionRef = useRef(false);

  const githubUrl = 'https://github.com/niczy/gitslice';
  const docsUrl = '/docs';
  const statusUrl = `${apiBaseUrl}/health`;
  const supportUrl = 'https://github.com/niczy/gitslice/issues';

  const webSessionQuery = useWebSession(initialSession);
  const slicesQuery = useSlicesQuery();
  const slices = slicesQuery.data || [];
  const slicesLoading = slicesQuery.isLoading;
  const slicesError = slicesQuery.error ? 'Unable to load slices.' : '';
  const currentSlice = slices.find((slice) => slice.slice_id === currentSliceId) || null;

  useEffect(() => {
    if (activePage === 'browser' || activePage === 'diff' || activePage === 'changeset') {
      setBrowserMounted(true);
    }
  }, [activePage]);

  const navigate = useCallback((page, commitHash = '', changesetId = '', options = {}) => {
    const nextPath = buildPath(page, commitHash, changesetId);
    if (routerNavigate) {
      routerNavigate(nextPath, options);
      return;
    }

    setActivePage(page);
    if (page === 'diff') {
      setDiffCommitHash(commitHash);
      setDiffChangesetId('');
    } else if (page === 'changeset') {
      setDiffCommitHash('');
      setDiffChangesetId(changesetId);
    } else {
      setDiffCommitHash('');
      setDiffChangesetId('');
    }
    setUnknownRoute('');
  }, [routerNavigate]);

  useEffect(() => {
    if (typeof window === 'undefined') {
      return;
    }
    const nextPath = buildLegacyRedirectPath(window.location);
    if (nextPath) {
      routerNavigate?.(nextPath, { replace: true });
    }
  }, [routerNavigate]);

  useEffect(() => {
    setActivePage(initialRoute.page);
    setDiffCommitHash(initialRoute.commitHash);
    setDiffChangesetId(initialRoute.changesetId);
    setUnknownRoute(initialRoute.unknownPath || '');
  }, [initialRoute]);

  useEffect(() => {
    if (slices.length === 0) {
      return;
    }
    setCurrentSliceId((prev) => {
      if (prev && slices.some((slice) => slice.slice_id === prev) && hasExplicitSliceSelectionRef.current) {
        return prev;
      }
      return getPreferredSliceId(slices, username);
    });
  }, [slices, username]);

  useEffect(() => {
    if (previousUsernameRef.current === username) {
      return;
    }
    previousUsernameRef.current = username;
    hasExplicitSliceSelectionRef.current = false;
    setCurrentSliceId(getHomeSliceId(username));
  }, [username]);

  useEffect(() => {
    const nextUsername = webSessionQuery.data?.user?.username || '';
    setUsername(nextUsername);
    setAuthSessionSource(webSessionQuery.data?.source || '');
    if (nextUsername && activePage === 'login') {
      const nextPage = returnToPage || 'browser';
      const nextCommitHash = nextPage === 'diff' ? returnToCommitHash : '';
      const nextChangesetId = nextPage === 'changeset' ? returnToChangesetId : '';
      navigate(nextPage, nextCommitHash, nextChangesetId);
    }
  }, [
    activePage,
    navigate,
    returnToChangesetId,
    returnToCommitHash,
    returnToPage,
    webSessionQuery.data,
  ]);

  const refreshSlices = useCallback(async () => {
    await slicesQuery.refetch();
  }, [slicesQuery]);

  const navigateToDiff = useCallback((commitHash) => {
    navigate('diff', commitHash);
  }, [navigate]);

  const navigateToChangesetDiff = useCallback((changesetId) => {
    navigate('changeset', '', changesetId);
  }, [navigate]);

  const openBrowserHome = useCallback(() => {
    hasExplicitSliceSelectionRef.current = false;
    setCurrentSliceId(getHomeSliceId(username));
    navigate('browser');
  }, [navigate, username]);

  const handleSliceChange = useCallback((sliceId) => {
    hasExplicitSliceSelectionRef.current = true;
    setCurrentSliceId(sliceId);
    setBrowserSearchMatches([]);
    setBrowserSearchError('');
    setBrowserSearchHasSearched(false);
  }, []);

  const handleBrowserSearchSubmit = useCallback(async (event) => {
    event.preventDefault();
    const query = browserSearchQuery.trim();
    if (!query) {
      setBrowserSearchError('Enter a search query.');
      setBrowserSearchMatches([]);
      setBrowserSearchHasSearched(false);
      return;
    }
    if (!currentSliceId || currentSlice?.is_root) {
      setBrowserSearchError('Choose a signed-in home or custom slice to search.');
      setBrowserSearchMatches([]);
      setBrowserSearchHasSearched(true);
      return;
    }

    setBrowserSearchLoading(true);
    setBrowserSearchError('');
    try {
      const payload = await searchWorkspaceFiles(currentSliceId, {
        query,
        glob: browserSearchGlob,
        regex: browserSearchRegex,
      });
      setBrowserSearchMatches((payload?.matches || []).map((match) => ({
        path: match?.path ?? '',
        line_number: match?.line_number ?? match?.lineNumber ?? 0,
        line: match?.line ?? '',
      })));
      setBrowserSearchHasSearched(true);
    } catch (err) {
      setBrowserSearchMatches([]);
      setBrowserSearchError(err?.message || 'Unable to search files.');
      setBrowserSearchHasSearched(true);
    } finally {
      setBrowserSearchLoading(false);
    }
  }, [browserSearchGlob, browserSearchQuery, browserSearchRegex, currentSlice?.is_root, currentSliceId]);

  const openBrowserSearchResult = useCallback((path) => {
    setBrowserOpenFileRequest({ path, token: Date.now() });
    navigate('browser');
  }, [navigate]);

  const navigateBackFromDiff = useCallback(() => {
    if (typeof window !== 'undefined' && window.history.length > 1) {
      window.history.back();
      return;
    }
    navigate('browser');
  }, [navigate]);

  const handleChangesetMerged = useCallback(() => {
    setHistoryRefreshToken((value) => value + 1);
    navigate('browser');
  }, [navigate]);

  const handleChangesetClosed = useCallback(() => {
    navigate('browser');
  }, [navigate]);

  const doLogout = useCallback(async () => {
    await signOutAccount();
    setUsername('');
    setAuthSessionSource('');
    navigate('landing', '', '', { replace: true });
    await queryClient.invalidateQueries({ queryKey: ['web-session'] });
    if (authSessionSource === 'workos' || authSessionSource === 'clerk') {
      startOAuthSignOut(authSessionSource);
    }
  }, [authSessionSource, navigate, queryClient]);

  const doLogin = useCallback(async (nextUsername) => {
    const signedInUsername = await signInWithAccount(apiBaseUrl, nextUsername);
    setUsername(signedInUsername);
    setCurrentSliceId(getHomeSliceId(signedInUsername));
    setAuthSessionSource('dev');
    await queryClient.invalidateQueries({ queryKey: ['web-session'] });
    await queryClient.invalidateQueries({ queryKey: ['slices'] });
  }, [queryClient]);

  const doOAuthLogin = useCallback((providerId) => {
    startOAuthSignIn(providerId);
  }, []);

  const openLogin = useCallback(() => {
    const provider = String(initialAuthConfig.authProvider || '').trim().toLowerCase();
    if (provider === 'workos' || provider === 'clerk') {
      startOAuthSignIn(provider);
      return;
    }
    navigate('login');
  }, [initialAuthConfig.authProvider, navigate]);

  const isAuthenticated = Boolean(username);
  const isBrowserLayout = activePage === 'browser' || activePage === 'diff' || activePage === 'changeset';
  const isAdminUser = (username || '').toLowerCase() === 'admin';
  const blockedProtectedPages = new Set(['projects', 'settings', 'profile', 'admin']);
  const isProtectedPage = blockedProtectedPages.has(activePage);
  const hasRouteAuthorization = activePage !== 'admin' || isAdminUser;
  const routeAccessState = !isProtectedPage
    ? 'allowed'
    : !isAuthenticated
      ? 'unauthenticated'
      : hasRouteAuthorization
        ? 'allowed'
        : 'unauthorized';

  useEffect(() => {
    if (isAuthenticated && activePage === 'landing') {
      navigate('browser', '', '', { replace: true });
    }
  }, [activePage, isAuthenticated, navigate]);

  useEffect(() => {
    if (activePage === 'not-found') {
      trackRouteEvent('route_not_found', {
        path: unknownRoute || '/',
      });
      return;
    }

    if (routeAccessState !== 'allowed') {
      trackRouteEvent('route_auth_blocked', {
        page: activePage,
        reason: routeAccessState,
      });
    }
  }, [activePage, routeAccessState, unknownRoute]);

  const handleGoToLogin = useCallback(() => {
    setReturnToPage(activePage);
    setReturnToCommitHash(diffCommitHash);
    setReturnToChangesetId(diffChangesetId);
    openLogin();
  }, [activePage, diffChangesetId, diffCommitHash, openLogin]);

  const isNavActive = (item) => {
    if (item === 'repos') {
      return activePage === 'browser' || activePage === 'diff' || activePage === 'changeset';
    }
    if (item === 'projects') {
      return activePage === 'projects';
    }
    if (item === 'settings') {
      return activePage === 'settings' || activePage === 'profile';
    }
    if (item === 'login') {
      return activePage === 'login';
    }
    if (item === 'docs') {
      return activePage === 'docs';
    }
    if (item === 'get-started') {
      return activePage === 'landing';
    }
    return false;
  };

  return (
    <div className={`app-shell min-h-screen bg-background text-foreground${isBrowserLayout ? ' app-shell--browser' : ''}`}>
      <AppHeader
        isAuthenticated={isAuthenticated}
        authSessionSource={authSessionSource}
        githubUrl={githubUrl}
        navigate={navigate}
        onOpenRepos={openBrowserHome}
        onLogin={openLogin}
        isNavActive={isNavActive}
        browserSearch={{
          visible: isBrowserLayout && isAuthenticated,
          query: browserSearchQuery,
          glob: browserSearchGlob,
          regex: browserSearchRegex,
          loading: browserSearchLoading,
          error: browserSearchError,
          matches: browserSearchMatches,
          empty: browserSearchHasSearched && !browserSearchLoading && !browserSearchError && browserSearchMatches.length === 0,
          onQueryChange: setBrowserSearchQuery,
          onGlobChange: setBrowserSearchGlob,
          onRegexChange: setBrowserSearchRegex,
          onSubmit: handleBrowserSearchSubmit,
          onOpenResult: openBrowserSearchResult,
        }}
      />

      <main className={`page${isBrowserLayout ? ' page--browser' : ''}`}>
        {activePage === 'landing' && (
          <OverviewPage
            onBrowseRepo={openBrowserHome}
            onOpenDocs={() => navigate('docs')}
          />
        )}
        {activePage === 'docs' && <DocsPage onBrowseRepo={openBrowserHome} />}
        {activePage === 'login' && (
          <LoginPage
            authProvider={initialAuthConfig.authProvider}
            allowDevLogin={initialAuthConfig.allowDevLogin}
            initialOAuthError={initialSessionError}
            onLogin={doLogin}
            onOAuthLogin={doOAuthLogin}
            onCancel={() => navigate('landing')}
            onLoggedIn={() => {
              const nextPage = returnToPage || 'browser';
              const nextCommitHash = nextPage === 'diff' ? returnToCommitHash : '';
              const nextChangesetId = nextPage === 'changeset' ? returnToChangesetId : '';
              navigate(nextPage, nextCommitHash, nextChangesetId);
            }}
          />
        )}
        {activePage === 'projects' && routeAccessState === 'allowed' && (
          <ProjectsPage
            slices={slices}
            slicesLoading={slicesLoading}
            slicesError={slicesError}
            onOpenRepos={openBrowserHome}
            onRefresh={refreshSlices}
          />
        )}
        {activePage === 'profile' && routeAccessState === 'allowed' && (
          <ProfilePage
            username={username}
            authSessionSource={authSessionSource}
            onLogout={doLogout}
            onRequireLogin={openLogin}
          />
        )}
        {activePage === 'settings' && routeAccessState === 'allowed' && (
          <SettingsPage
            username={username}
            authSessionSource={authSessionSource}
            onOpenProfile={() => navigate('profile')}
            onLogout={doLogout}
          />
        )}

        {browserMounted && routeAccessState === 'allowed' && (
          <div style={activePage !== 'browser' ? { display: 'none' } : undefined}>
            <RepoBrowser
              slices={slices}
              currentSliceId={currentSliceId}
              authUsername={username}
              onSliceChange={handleSliceChange}
              onNavigateToDiff={navigateToDiff}
              refreshHistoryToken={historyRefreshToken}
              isActive={activePage === 'browser'}
              slicesLoading={slicesLoading}
              slicesError={slicesError}
              onRefreshSlices={refreshSlices}
              openFileRequest={browserOpenFileRequest}
            />
          </div>
        )}

        {activePage === 'diff' && routeAccessState === 'allowed' && (
          <CommitDiffPage
            commitHash={diffCommitHash}
            onBack={navigateBackFromDiff}
            onOpenChangesetDiff={navigateToChangesetDiff}
          />
        )}

        {activePage === 'changeset' && routeAccessState === 'allowed' && (
          <ChangesetDiffPage
            changesetId={diffChangesetId}
            onBack={navigateBackFromDiff}
            onMerged={handleChangesetMerged}
            onClosed={handleChangesetClosed}
          />
        )}

        {activePage === 'admin' && routeAccessState === 'allowed' && <AdminPage />}

        {isProtectedPage && routeAccessState !== 'allowed' && (
          <RouteAccessState
            state={routeAccessState}
            onGoToLogin={handleGoToLogin}
          />
        )}

        {activePage === 'not-found' && <NotFoundPage unknownPath={unknownRoute} onGoHome={() => navigate('landing')} />}
      </main>

      <AppFooter docsUrl={docsUrl} statusUrl={statusUrl} supportUrl={supportUrl} githubUrl={githubUrl} />
    </div>
  );
}

export default App;
