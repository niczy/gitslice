import { Badge } from './ui/badge.jsx';
import { Button } from './ui/button.jsx';
import { Card, CardContent } from './ui/card.jsx';

export default function SettingsPage({ username, onOpenProfile }) {
  return (
    <section className="section space-y-4" data-testid="settings-page">
      <div className="section-header">
        <Badge variant="secondary" className="eyebrow">Account</Badge>
        <h2>Settings</h2>
        <p>Manage account-level preferences and organization details.</p>
      </div>

      {!username && <div className="panel-error">You need to log in before account settings are available.</div>}
      {username && (
        <Card className="border-border/70">
          <CardContent className="space-y-4 pt-6">
            <div className="panel-empty" data-testid="settings-empty-state">
              Advanced settings are not configured yet for this account.
            </div>
            <div className="auth-actions">
              <Button type="button" variant="ghost" onClick={onOpenProfile}>Open Profile Details</Button>
            </div>
          </CardContent>
        </Card>
      )}
    </section>
  );
}
