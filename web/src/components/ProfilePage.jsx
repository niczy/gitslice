import { UserProfile } from '@clerk/react-router';

import { Button } from './ui/button.jsx';
import { Card, CardContent, CardHeader, CardTitle } from './ui/card.jsx';

export default function ProfilePage({ username, authSessionSource, onLogout }) {
  return (
    <section className="section auth-page profile-page" data-testid="profile-page">
      {authSessionSource === 'clerk' && (
        <div className="clerk-profile-section">
          <div className="clerk-profile-surface">
            <UserProfile path="/profile" routing="path" />
          </div>
        </div>
      )}

      {authSessionSource !== 'clerk' && (
        <Card className="auth-card border-border/70">
          <CardHeader>
            <CardTitle className="text-xl">Profile</CardTitle>
          </CardHeader>
          <CardContent className="space-y-4">
            <p className="status">{username ? `Logged in as ${username}.` : 'Not logged in.'}</p>
            <Button type="button" variant="ghost" onClick={onLogout}>
              Logout
            </Button>
          </CardContent>
        </Card>
      )}
    </section>
  );
}
