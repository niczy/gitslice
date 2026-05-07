import { useCallback, useEffect, useLayoutEffect, useRef, useState } from 'react';
import { useQueryClient } from '@tanstack/react-query';

import { buildBrowserPath, buildLegacyRedirectPath, buildPath, resolveHomeRouteForUsername } from './utils/routing.js';
import { apiBaseUrl, currentUsername, searchWorkspaceFiles } from './utils/api.js';
import { completeClerkUsername, signInWithAccount, signOutAccount, startOAuthSignIn, startOAuthSignOut } from './auth.js';
import { useWebSession } from './hooks/useWebSession.js';
import { useSlicesQuery } from './hooks/useSlices.js';

import OverviewPage from './components/OverviewPage.jsx';
import DocsPage from './components/DocsPage.jsx';
import AppHeader from './components/AppHeader.jsx';
import AppFooter from './components/AppFooter.jsx';
import AdminPage from './components/AdminPage.jsx';
import LoginPage from './components/LoginPage.jsx';
import UsernameOnboardingPage from './components/UsernameOnboardingPage.jsx';
import ProfilePage from './components/ProfilePage.jsx';
import RepoBrowser from './components/RepoBrowser.jsx';
import SliceHomePage from './components/SliceHomePage.jsx';
import SliceCommitListPage from './components/SliceCommitListPage.jsx';
import SliceChangesetListPage from './components/SliceChangesetListPage.jsx';
import ProjectsPage from './components/ProjectsPage.jsx';
import SettingsPage from './components/SettingsPage.jsx';
import NotFoundPage from './components/NotFoundPage.jsx';
import CommitDiffPage from './components/CommitDiffPage.jsx';
import ChangesetDiffPage from './components/ChangesetDiffPage.jsx';
import RouteAccessState from './components/RouteAccessState.jsx';
import { trackRouteEvent } from './utils/analytics.js';
import { docsMarkdown } from './docs.js';

const useIsomorphicLayoutEffect = typeof window === 'undefined' ? useEffect : useLayoutEffect;

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

function getInitialSliceId(username) {
  return getHomeSliceId(username) || 'root_slice';
}

function isSliceScopedPage(page) {
  return page === 'browser' || page === 'slice-commits' || page === 'slice-changesets';
}

function App({
  initialRoute,
  initialAuthConfig = { authProvider: 'local', allowDevLogin: true },
  initialSession = null,
  initialSessionError = '',
  initialBrowserData = null,
  routerNavigate,
}) {
  const queryClient = useQueryClient();
  const initialRouteData = initialBrowserData || {};
  const initialUsername = initialSession?.user?.username || currentUsername();
  const resolvedInitialRoute = resolveHomeRouteForUsername(initialRoute, initialUsername);
  const initialBrowserRouteSlice = isSliceScopedPage(resolvedInitialRoute.page) ? resolvedInitialRoute.browserState?.slice || '' : '';
  const initialPage = resolvedInitialRoute.page;
  const [activePage, setActivePage] = useState(() => initialPage);
  const [diffCommitHash, setDiffCommitHash] = useState(() => resolvedInitialRoute.commitHash);
  const [diffChangesetId, setDiffChangesetId] = useState(() => resolvedInitialRoute.changesetId);
  const [unknownRoute, setUnknownRoute] = useState(() => resolvedInitialRoute.unknownPath || '');
  const [returnToPage, setReturnToPage] = useState('browser');
  const [returnToCommitHash, setReturnToCommitHash] = useState('');
  const [returnToChangesetId, setReturnToChangesetId] = useState('');
  const [username, setUsername] = useState(() => initialUsername);
  const [authSessionSource, setAuthSessionSource] = useState(() => initialSession?.source || '');
  const [requiresUsername, setRequiresUsername] = useState(() => Boolean(initialSession?.requiresUsername && !initialUsername));
  const [pendingClerkUser, setPendingClerkUser] = useState(() => initialSession?.user || null);
  const [browserRouteSliceId, setBrowserRouteSliceId] = useState(() => initialBrowserRouteSlice);
  const [browserMounted, setBrowserMounted] = useState(() => initialPage === 'browser' && Boolean(initialBrowserRouteSlice));
  const [currentSliceId, setCurrentSliceId] = useState(() => initialBrowserRouteSlice || getInitialSliceId(initialUsername));
  const [historyRefreshToken, setHistoryRefreshToken] = useState(0);
  const [browserSearchQuery, setBrowserSearchQuery] = useState('');
  const [browserSearchGlob, setBrowserSearchGlob] = useState('');
  const [browserSearchRegex, setBrowserSearchRegex] = useState(false);
  const [browserSearchMatches, setBrowserSearchMatches] = useState([]);
  const [browserSearchLoading, setBrowserSearchLoading] = useState(false);
  const [browserSearchError, setBrowserSearchError] = useState('');
  const [browserSearchHasSearched, setBrowserSearchHasSearched] = useState(false);
  const [browserOpenFileRequest, setBrowserOpenFileRequest] = useState(null);
  const browserSearchRequestRef = useRef(0);
  const previousUsernameRef = useRef(username);
  const hasExplicitSliceSelectionRef = useRef(Boolean(initialBrowserRouteSlice));

  const githubUrl = 'https://github.com/niczy/gitslice';
  const docsUrl = '/docs';
  const statusUrl = `${apiBaseUrl}/health`;
  const supportUrl = 'https://github.com/niczy/gitslice/issues';

  const webSessionQuery = useWebSession(initialSession);
  const slicesQuery = useSlicesQuery(initialRouteData?.slices);
  const slices = slicesQuery.data || [];
  const slicesLoading = slicesQuery.isLoading;
  const slicesError = initialRouteData?.slicesError || (slicesQuery.error ? 'Unable to load slices.' : '');
  const currentSlice = slices.find((slice) => slice.slice_id === currentSliceId) || null;

  const isBrowserDetail = activePage === 'browser' && Boolean(browserRouteSliceId);
  const isSliceScopedDetail = isSliceScopedPage(activePage) && Boolean(browserRouteSliceId);

  useEffect(() => {
    if (isBrowserDetail || activePage === 'diff' || activePage === 'changeset') {
      setBrowserMounted(true);
    }
  }, [activePage, isBrowserDetail]);

  const navigate = useCallback((page, commitHash = '', changesetId = '', options = {}) => {
    const nextPath = buildPath(page, commitHash, changesetId, options.browserState);
    const navigateOptions = { ...options };
    delete navigateOptions.browserState;
    if (routerNavigate) {
      routerNavigate(nextPath, navigateOptions);
      return;
    }

    setActivePage(page);
    if (page === 'diff') {
      setDiffCommitHash(commitHash);
      setDiffChangesetId('');
    } else if (page === 'changeset') {
      setDiffCommitHash('');
      setDiffChangesetId(changesetId);
    } else if (page === 'slice-commits' || page === 'slice-changesets') {
      const nextSliceId = options.browserState?.slice || '';
      setDiffCommitHash('');
      setDiffChangesetId('');
      setBrowserRouteSliceId(nextSliceId);
      if (nextSliceId) {
        setCurrentSliceId(nextSliceId);
      }
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

  useIsomorphicLayoutEffect(() => {
    const nextRoute = resolveHomeRouteForUsername(initialRoute, username);
    setActivePage(nextRoute.page);
    setDiffCommitHash(nextRoute.commitHash);
    setDiffChangesetId(nextRoute.changesetId);
    setUnknownRoute(nextRoute.unknownPath || '');
    if (isSliceScopedPage(nextRoute.page)) {
      const nextRouteSliceId = nextRoute.browserState?.slice || '';
      setBrowserRouteSliceId(nextRouteSliceId);
      if (nextRouteSliceId) {
        hasExplicitSliceSelectionRef.current = true;
        setCurrentSliceId(nextRouteSliceId);
        if (nextRoute.page === 'browser') {
          setBrowserMounted(true);
        }
      } else {
        hasExplicitSliceSelectionRef.current = false;
      }
    } else {
      setBrowserRouteSliceId('');
    }
  }, [initialRoute, username]);

  useEffect(() => {
    if (slices.length === 0) {
      return;
    }
    setCurrentSliceId((prev) => {
      if (prev && hasExplicitSliceSelectionRef.current) {
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
    setCurrentSliceId(getInitialSliceId(username));
  }, [username]);

  useEffect(() => {
    const session = webSessionQuery.data || null;
    const nextUsername = session?.user?.username || '';
    const nextRequiresUsername = Boolean(session?.requiresUsername && session?.source === 'clerk' && !nextUsername);
    setUsername(nextUsername);
    setAuthSessionSource(session?.source || '');
    setRequiresUsername(nextRequiresUsername);
    if (nextRequiresUsername) {
      setPendingClerkUser(session?.user || null);
    } else if (nextUsername) {
      setPendingClerkUser(null);
    }
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

  const openSliceDetail = useCallback((sliceId, browserState = {}) => {
    const normalizedSliceId = String(sliceId || '').trim();
    if (!normalizedSliceId) {
      return;
    }

    setBrowserSearchMatches([]);
    setBrowserSearchError('');
    setBrowserSearchHasSearched(false);
    setDiffCommitHash('');
    setDiffChangesetId('');
    setUnknownRoute('');

    const nextPath = buildBrowserPath({
      ...browserState,
      slice: normalizedSliceId,
    });
    if (routerNavigate) {
      hasExplicitSliceSelectionRef.current = true;
      routerNavigate(nextPath);
      return;
    }

    hasExplicitSliceSelectionRef.current = true;
    setCurrentSliceId(normalizedSliceId);
    setBrowserRouteSliceId(normalizedSliceId);
    setActivePage('browser');
  }, [routerNavigate]);

  const openSliceCommits = useCallback((sliceId = currentSliceId) => {
    const normalizedSliceId = String(sliceId || '').trim();
    if (!normalizedSliceId) {
      return;
    }
    hasExplicitSliceSelectionRef.current = true;
    if (routerNavigate) {
      navigate('slice-commits', '', '', { browserState: { slice: normalizedSliceId } });
      return;
    }
    setCurrentSliceId(normalizedSliceId);
    setBrowserRouteSliceId(normalizedSliceId);
    navigate('slice-commits', '', '', { browserState: { slice: normalizedSliceId } });
  }, [currentSliceId, navigate, routerNavigate]);

  const openSliceChangesets = useCallback((sliceId = currentSliceId) => {
    const normalizedSliceId = String(sliceId || '').trim();
    if (!normalizedSliceId) {
      return;
    }
    hasExplicitSliceSelectionRef.current = true;
    if (routerNavigate) {
      navigate('slice-changesets', '', '', { browserState: { slice: normalizedSliceId } });
      return;
    }
    setCurrentSliceId(normalizedSliceId);
    setBrowserRouteSliceId(normalizedSliceId);
    navigate('slice-changesets', '', '', { browserState: { slice: normalizedSliceId } });
  }, [currentSliceId, navigate, routerNavigate]);

  const openBrowserHome = useCallback(() => {
    hasExplicitSliceSelectionRef.current = false;
    if (routerNavigate) {
      navigate('browser');
      return;
    }
    setCurrentSliceId(getInitialSliceId(username));
    setBrowserRouteSliceId('');
    setActivePage('browser');
    setDiffCommitHash('');
    setDiffChangesetId('');
    setUnknownRoute('');
    navigate('browser');
  }, [navigate, routerNavigate, username]);

  const handleSliceChange = useCallback((sliceId) => {
    openSliceDetail(sliceId);
  }, [openSliceDetail]);

  const runBrowserSearch = useCallback(async ({ showBlankError = false, signal = undefined } = {}) => {
    const query = browserSearchQuery.trim();
    if (!query) {
      browserSearchRequestRef.current += 1;
      setBrowserSearchError(showBlankError ? 'Enter a search query.' : '');
      setBrowserSearchMatches([]);
      setBrowserSearchHasSearched(false);
      return;
    }
    if (!currentSliceId || currentSlice?.is_root) {
      browserSearchRequestRef.current += 1;
      setBrowserSearchError('Choose a signed-in home or custom slice to search.');
      setBrowserSearchMatches([]);
      setBrowserSearchHasSearched(true);
      return;
    }

    const requestId = browserSearchRequestRef.current + 1;
    browserSearchRequestRef.current = requestId;
    setBrowserSearchLoading(true);
    setBrowserSearchError('');
    try {
      const payload = await searchWorkspaceFiles(currentSliceId, {
        query,
        glob: browserSearchGlob,
        regex: browserSearchRegex,
        signal,
      });
      if (requestId !== browserSearchRequestRef.current) {
        return;
      }
      setBrowserSearchMatches((payload?.matches || []).map((match) => ({
        path: match?.path ?? '',
        line_number: match?.line_number ?? match?.lineNumber ?? 0,
        line: match?.line ?? '',
        match_start: match?.match_start ?? match?.matchStart ?? 0,
        match_end: match?.match_end ?? match?.matchEnd ?? 0,
      })));
      setBrowserSearchHasSearched(true);
    } catch (err) {
      if (err?.name === 'AbortError' || requestId !== browserSearchRequestRef.current) {
        return;
      }
      setBrowserSearchMatches([]);
      setBrowserSearchError(err?.message || 'Unable to search files.');
      setBrowserSearchHasSearched(true);
    } finally {
      if (requestId === browserSearchRequestRef.current) {
        setBrowserSearchLoading(false);
      }
    }
  }, [browserSearchGlob, browserSearchQuery, browserSearchRegex, currentSlice?.is_root, currentSliceId]);

  const handleBrowserSearchSubmit = useCallback((event) => {
    event.preventDefault();
    runBrowserSearch({ showBlankError: true });
  }, [runBrowserSearch]);

  const openBrowserSearchResult = useCallback((path) => {
    setBrowserOpenFileRequest({ path, token: Date.now() });
    openSliceDetail(currentSliceId, { file: path });
  }, [currentSliceId, openSliceDetail]);

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

  const doCompleteClerkUsername = useCallback(async (chosenUsername) => {
    const session = await completeClerkUsername(chosenUsername);
    const nextUsername = session?.user?.username || '';
    if (!nextUsername) {
      throw new Error('Username was not saved.');
    }
    setUsername(nextUsername);
    setAuthSessionSource(session?.source || 'clerk');
    setRequiresUsername(false);
    setPendingClerkUser(null);
    setCurrentSliceId(getHomeSliceId(nextUsername));
    queryClient.setQueryData(['web-session'], session);
    await queryClient.invalidateQueries({ queryKey: ['slices'] });
    if (activePage === 'login') {
      navigate('browser', '', '', { replace: true });
    }
  }, [activePage, navigate, queryClient]);

  const openLogin = useCallback(() => {
    const provider = String(initialAuthConfig.authProvider || '').trim().toLowerCase();
    if (provider === 'workos' || provider === 'clerk') {
      startOAuthSignIn(provider);
      return;
    }
    navigate('login');
  }, [initialAuthConfig.authProvider, navigate]);

  const isClerkUsernameRequired = authSessionSource === 'clerk' && requiresUsername && !username;
  const isAuthenticated = Boolean(username);
  const hasSignedInShell = isAuthenticated || isClerkUsernameRequired;
  const isAdminUser = Boolean(webSessionQuery.data?.user?.isAdmin || initialSession?.user?.isAdmin);
  const isSliceHomePage = activePage === 'browser' && !browserRouteSliceId;
  const isBrowserLayout = (activePage === 'browser' && Boolean(browserRouteSliceId)) || isSliceScopedDetail || activePage === 'diff' || activePage === 'changeset';
  const pageClassName = `page${isBrowserLayout ? ' page--browser' : ''}${isSliceHomePage ? ' page--slice-home' : ''}${activePage === 'profile' ? ' page--profile' : ''}`;
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
    const query = browserSearchQuery.trim();
    if (!isBrowserDetail || !isAuthenticated || !query) {
      browserSearchRequestRef.current += 1;
      setBrowserSearchLoading(false);
      if (!query) {
        setBrowserSearchMatches([]);
        setBrowserSearchError('');
        setBrowserSearchHasSearched(false);
      }
      return undefined;
    }

    const controller = new AbortController();
    const timer = window.setTimeout(() => {
      runBrowserSearch({ signal: controller.signal });
    }, 250);

    return () => {
      window.clearTimeout(timer);
      controller.abort();
    };
  }, [browserSearchGlob, browserSearchQuery, browserSearchRegex, isAuthenticated, isBrowserDetail, runBrowserSearch]);

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
      return activePage === 'browser' || activePage === 'slice-commits' || activePage === 'slice-changesets' || activePage === 'diff' || activePage === 'changeset';
    }
    if (item === 'projects') {
      return activePage === 'projects';
    }
    if (item === 'settings') {
      return activePage === 'settings' || activePage === 'profile';
    }
    if (item === 'profile') {
      return activePage === 'profile';
    }
    if (item === 'admin') {
      return activePage === 'admin';
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
        isAuthenticated={hasSignedInShell}
        authSessionSource={authSessionSource}
        username={username}
        githubUrl={githubUrl}
        navigate={navigate}
        onOpenRepos={openBrowserHome}
        onLogin={openLogin}
        isAdminUser={isAdminUser}
        isNavActive={isNavActive}
        browserSearch={{
          visible: isBrowserDetail && isAuthenticated,
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

      <main className={pageClassName}>
        {isClerkUsernameRequired ? (
          <UsernameOnboardingPage
            suggestedUsername={pendingClerkUser?.suggestedUsername || pendingClerkUser?.derivedUsername || ''}
            email={pendingClerkUser?.email || ''}
            onSubmit={doCompleteClerkUsername}
            onLogout={doLogout}
          />
        ) : (
          <>
        {activePage === 'landing' && (
          <OverviewPage
            onBrowseRepo={openBrowserHome}
            onOpenDocs={() => navigate('docs')}
          />
        )}
        {activePage === 'docs' && <DocsPage markdown={docsMarkdown} onBrowseRepo={openBrowserHome} />}
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
            initialSettingsData={initialRouteData.settings}
          />
        )}

        {activePage === 'browser' && !browserRouteSliceId && routeAccessState === 'allowed' && (
          <SliceHomePage
            slices={slices}
            slicesLoading={slicesLoading}
            slicesError={slicesError}
            isAuthenticated={isAuthenticated}
            username={username}
            homeSliceId={getHomeSliceId(username)}
            onOpenSlice={openSliceDetail}
            onRefresh={refreshSlices}
            onRequireLogin={openLogin}
          />
        )}

        {browserMounted && browserRouteSliceId && routeAccessState === 'allowed' && (
          <div style={activePage !== 'browser' ? { display: 'none' } : undefined}>
            <RepoBrowser
              slices={slices}
              currentSliceId={currentSliceId}
              authUsername={username}
              publicApiBaseUrl={initialAuthConfig.publicApiBaseUrl || ''}
              onSliceChange={handleSliceChange}
              onNavigateToDiff={navigateToDiff}
              onOpenCommits={() => openSliceCommits(currentSliceId)}
              onOpenChangesets={() => openSliceChangesets(currentSliceId)}
              refreshHistoryToken={historyRefreshToken}
              isActive={activePage === 'browser'}
              slicesLoading={slicesLoading}
              openFileRequest={browserOpenFileRequest}
              initialBrowserData={initialBrowserData}
            />
          </div>
        )}

        {activePage === 'slice-commits' && routeAccessState === 'allowed' && (
          <SliceCommitListPage
            sliceId={browserRouteSliceId || currentSliceId}
            slices={slices}
            publicApiBaseUrl={initialAuthConfig.publicApiBaseUrl || ''}
            onOpenCode={() => openSliceDetail(browserRouteSliceId || currentSliceId)}
            onOpenChangesets={() => openSliceChangesets(browserRouteSliceId || currentSliceId)}
            onOpenCommitDiff={navigateToDiff}
            initialCommits={initialRouteData.sliceCommits}
            initialCommitsError={initialRouteData.sliceCommitsError || ''}
            initialCommitsHasMore={Boolean(initialRouteData.sliceCommitsHasMore)}
            initialCommitsSliceId={initialRouteData.sliceCommitsSliceId || ''}
          />
        )}

        {activePage === 'slice-changesets' && routeAccessState === 'allowed' && (
          <SliceChangesetListPage
            sliceId={browserRouteSliceId || currentSliceId}
            slices={slices}
            publicApiBaseUrl={initialAuthConfig.publicApiBaseUrl || ''}
            onOpenCode={() => openSliceDetail(browserRouteSliceId || currentSliceId)}
            onOpenCommits={() => openSliceCommits(browserRouteSliceId || currentSliceId)}
            onOpenChangesetDiff={navigateToChangesetDiff}
            initialChangesets={initialRouteData.sliceChangesets}
            initialChangesetsError={initialRouteData.sliceChangesetsError || ''}
            initialChangesetsSliceId={initialRouteData.sliceChangesetsSliceId || ''}
            initialStatusFilter={initialRouteData.sliceChangesetsStatusFilter || 'all'}
          />
        )}

        {activePage === 'diff' && routeAccessState === 'allowed' && (
          <CommitDiffPage
            commitHash={diffCommitHash}
            onBack={navigateBackFromDiff}
            onOpenChangesetDiff={navigateToChangesetDiff}
            initialCommitHash={initialRouteData.commitDiffHash || ''}
            initialDiffData={initialRouteData.commitDiff}
            initialDiffError={initialRouteData.commitDiffError || ''}
          />
        )}

        {activePage === 'changeset' && routeAccessState === 'allowed' && (
          <ChangesetDiffPage
            changesetId={diffChangesetId}
            onBack={navigateBackFromDiff}
            onMerged={handleChangesetMerged}
            onClosed={handleChangesetClosed}
            initialChangesetId={initialRouteData.changesetId || ''}
            initialSnapshots={initialRouteData.changesetSnapshots}
            initialSnapshotsError={initialRouteData.changesetSnapshotsError || ''}
            initialSnapshotVersion={initialRouteData.changesetSnapshotVersion || 0}
            initialDiffData={initialRouteData.changesetDiff}
            initialDiffError={initialRouteData.changesetDiffError || ''}
          />
        )}

        {activePage === 'admin' && routeAccessState === 'allowed' && <AdminPage initialIsAdmin={isAdminUser} />}

        {isProtectedPage && routeAccessState !== 'allowed' && (
          <RouteAccessState
            state={routeAccessState}
            onGoToLogin={handleGoToLogin}
          />
        )}

        {activePage === 'not-found' && <NotFoundPage unknownPath={unknownRoute} onGoHome={() => navigate('landing')} />}
          </>
        )}
      </main>

      <AppFooter docsUrl={docsUrl} statusUrl={statusUrl} supportUrl={supportUrl} githubUrl={githubUrl} />
    </div>
  );
}

export default App;
