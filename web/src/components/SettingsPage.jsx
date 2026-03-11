import { Badge } from './ui/badge.jsx';
import { Button } from './ui/button.jsx';
import { Card, CardContent } from './ui/card.jsx';

export default function SettingsPage({ username, authSessionSource, onOpenProfile, onLogout }) {
  return (
    <section className="section space-y-4" data-testid="settings-page">
      <div className="section-header">
        <Badge variant="secondary" className="eyebrow">Account</Badge>
        <h2>Settings</h2>
        <p>Manage session-level preferences, inspect how you are authenticated, and jump into profile details.</p>
      </div>

      {!username && <div className="panel-error">You need to log in before account settings are available.</div>}
      {username && (
        <>
          <div className="grid gap-4 md:grid-cols-2">
            <Card className="border-border/70">
              <CardContent className="space-y-4 pt-6">
                <div className="kv">
                  <div className="kv-row">
                    <span className="kv-key">Signed in as</span>
                    <span className="kv-val">{username}</span>
                  </div>
                  <div className="kv-row">
                    <span className="kv-key">Auth mode</span>
                    <span className="kv-val">{authSessionSource || 'unknown'}</span>
                  </div>
                </div>
                <div className="auth-actions">
                  <Button type="button" onClick={onOpenProfile}>Open Profile Details</Button>
                  <Button type="button" variant="ghost" onClick={onLogout}>Logout</Button>
                </div>
              </CardContent>
            </Card>

            <Card className="border-border/70">
              <CardContent className="space-y-4 pt-6">
                <div className="panel-empty" data-testid="settings-empty-state">
                  UI preferences are still light here, but the session plumbing is now cookie-backed and ready for fuller account settings.
                </div>
                <div className="status">Next sensible additions: editor preferences, preferred agent provider, and workspace defaults.</div>
              </CardContent>
            </Card>
          </div>
        </>
      )}
    </section>
  );
}
