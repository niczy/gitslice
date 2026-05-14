import { Button } from '../ui/button.jsx';
import { Card, CardContent } from '../ui/card.jsx';

export default function AuthContextCard({
  authContext,
  authContextError,
  authContextLoading,
  authSessionSource,
  onLogout,
  onOpenProfile,
  onRefreshAuthContext,
  onRefreshAuthMethods,
  onRefreshSessions,
  username,
}) {
  return (
    <Card className="border-border/70">
      <CardContent className="space-y-4 pt-6">
        <div className="kv" data-testid="settings-auth-context">
          <div className="kv-row">
            <span className="kv-key">Signed in as</span>
            <span className="kv-val">{username}</span>
          </div>
          <div className="kv-row">
            <span className="kv-key">Browser sign-in</span>
            <span className="kv-val">{authSessionSource || 'unknown'}</span>
          </div>
          {authContextLoading && (
            <div className="kv-row">
              <span className="kv-key">API credential</span>
              <span className="kv-val">loading...</span>
            </div>
          )}
          {!authContextLoading && authContextError && (
            <div className="kv-row">
              <span className="kv-key">API credential</span>
              <span className="kv-val text-destructive">{authContextError}</span>
            </div>
          )}
          {!authContextLoading && !authContextError && authContext && (
            <>
              <div className="kv-row">
                <span className="kv-key">API credential</span>
                <span className="kv-val">{authContext.auth_source || authContext.authSource || 'unknown'}</span>
              </div>
              <div className="kv-row">
                <span className="kv-key">Session ID</span>
                <span className="kv-val break-all">{authContext.session_id || authContext.sessionId || 'none'}</span>
              </div>
              <div className="kv-row">
                <span className="kv-key">Agent key</span>
                <span className="kv-val">{authContext.agent_key_id || authContext.agentKeyId || 'none'}</span>
              </div>
              <div className="kv-row">
                <span className="kv-key">Clerk linked</span>
                <span className="kv-val">{authContext.clerk_linked || authContext.clerkLinked ? 'yes' : 'no'}</span>
              </div>
            </>
          )}
        </div>
        <div className="auth-actions">
          <Button type="button" onClick={onOpenProfile}>Open Profile Details</Button>
          <Button type="button" variant="ghost" onClick={onRefreshAuthContext}>Refresh auth context</Button>
          <Button type="button" variant="ghost" onClick={onRefreshAuthMethods}>Refresh auth methods</Button>
          <Button type="button" variant="ghost" onClick={onRefreshSessions}>Refresh sessions</Button>
          <Button type="button" variant="ghost" onClick={onLogout}>Logout</Button>
        </div>
      </CardContent>
    </Card>
  );
}
