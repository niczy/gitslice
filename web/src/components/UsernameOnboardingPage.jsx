import { useMemo, useState } from 'react';
import { AtSign } from 'lucide-react';
import { Badge } from './ui/badge.jsx';
import { Button } from './ui/button.jsx';
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from './ui/card.jsx';
import { Input } from './ui/input.jsx';

const USERNAME_PATTERN = /^[a-z0-9][a-z0-9_-]{2,31}$/;

function normalizeUsername(value) {
  return String(value || '').trim().toLowerCase();
}

function validateUsername(value) {
  const username = normalizeUsername(value);
  if (!username) {
    return 'Username is required.';
  }
  if (!USERNAME_PATTERN.test(username)) {
    return 'Use 3-32 lowercase letters, numbers, "_" or "-", starting with a letter or number.';
  }
  return '';
}

export default function UsernameOnboardingPage({
  suggestedUsername = '',
  email = '',
  onSubmit,
  onLogout,
}) {
  const [value, setValue] = useState(() => normalizeUsername(suggestedUsername));
  const [error, setError] = useState('');
  const [loading, setLoading] = useState(false);
  const validationError = useMemo(() => validateUsername(value), [value]);
  const normalizedValue = normalizeUsername(value);

  return (
    <section className="section auth-page" data-testid="username-onboarding-page">
      <div className="section-header">
        <Badge variant="secondary" className="eyebrow">Account setup</Badge>
        <h1>Choose your username</h1>
        <p>Your home slice will use this username as its slug.</p>
      </div>

      <form
        className="auth-card auth-card--username username-onboarding-card"
        onSubmit={async (event) => {
          event.preventDefault();
          setError('');
          const nextError = validateUsername(value);
          if (nextError) {
            setError(nextError);
            return;
          }
          setLoading(true);
          try {
            await onSubmit?.(normalizedValue);
          } catch (err) {
            setError(err?.message || 'Unable to choose username.');
          } finally {
            setLoading(false);
          }
        }}
      >
        <Card className="border-border/70">
          <CardHeader>
            <CardTitle className="auth-card-title text-xl">Gitslice username</CardTitle>
            <CardDescription className="auth-card-subtitle">
              {email ? `Signed in as ${email}.` : 'Signed in with Clerk.'}
            </CardDescription>
          </CardHeader>
          <CardContent className="space-y-4">
            <label className="field space-y-2">
              <span className="field-label">Username</span>
              <div className="username-field-control">
                <AtSign size={16} aria-hidden="true" />
                <Input
                  type="text"
                  value={value}
                  onChange={(event) => {
                    setValue(event.target.value.toLowerCase());
                    setError('');
                  }}
                  placeholder="nic"
                  autoFocus
                  spellCheck={false}
                  autoComplete="username"
                  aria-invalid={Boolean(error || (value && validationError))}
                  aria-describedby="username-onboarding-help username-onboarding-error"
                />
              </div>
              <span id="username-onboarding-help" className="field-help">
                Your home slice slug will be {normalizedValue || 'username'}.
              </span>
            </label>

            {(error || (value && validationError)) && (
              <div id="username-onboarding-error" className="panel-error" role="alert">
                {error || validationError}
              </div>
            )}

            <div className="auth-actions flex items-center gap-2">
              <Button type="submit" disabled={loading || Boolean(validationError)}>
                {loading ? 'Saving...' : 'Save username'}
              </Button>
              <Button type="button" variant="ghost" onClick={onLogout}>
                Sign out
              </Button>
            </div>
          </CardContent>
        </Card>
      </form>
    </section>
  );
}
