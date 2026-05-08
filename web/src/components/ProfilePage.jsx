import { useEffect, useState } from 'react';
import { UserProfile } from '@clerk/react-router';

import { Button } from './ui/button.jsx';
import { Card, CardContent, CardHeader, CardTitle } from './ui/card.jsx';
import { Input } from './ui/input.jsx';
import { deleteCurrentUser, fetchCurrentUser, updateCurrentUser } from '../utils/api.js';

export default function ProfilePage({ username, authSessionSource, onLogout }) {
  const [name, setName] = useState('');
  const [primaryEmail, setPrimaryEmail] = useState('');
  const [loading, setLoading] = useState(false);
  const [saving, setSaving] = useState(false);
  const [deleting, setDeleting] = useState(false);
  const [error, setError] = useState('');
  const [success, setSuccess] = useState('');
  const [deleteConfirmation, setDeleteConfirmation] = useState('');

  useEffect(() => {
    let cancelled = false;
    if (!username || authSessionSource === 'clerk') {
      setName('');
      setPrimaryEmail('');
      setLoading(false);
      setError('');
      setSuccess('');
      setDeleteConfirmation('');
      return () => {
        cancelled = true;
      };
    }

    setLoading(true);
    setError('');
    setSuccess('');
    fetchCurrentUser()
      .then((user) => {
        if (cancelled) {
          return;
        }
        setName(user?.name || '');
        setPrimaryEmail(user?.primary_email || user?.primaryEmail || '');
      })
      .catch((err) => {
        if (!cancelled) {
          setError(err?.message || 'Unable to load profile.');
        }
      })
      .finally(() => {
        if (!cancelled) {
          setLoading(false);
        }
      });

    return () => {
      cancelled = true;
    };
  }, [authSessionSource, username]);

  async function handleSave(event) {
    event.preventDefault();
    setError('');
    setSuccess('');
    setSaving(true);
    try {
      const updated = await updateCurrentUser({ name, primaryEmail });
      setName(updated?.name || '');
      setPrimaryEmail(updated?.primary_email || updated?.primaryEmail || '');
      setSuccess('Profile updated.');
    } catch (err) {
      setError(err?.message || 'Unable to update profile.');
    } finally {
      setSaving(false);
    }
  }

  async function handleDelete() {
    setError('');
    setSuccess('');
    if (deleteConfirmation !== username) {
      setError('Type your username to confirm account deletion.');
      return;
    }
    setDeleting(true);
    try {
      await deleteCurrentUser();
      await onLogout?.();
    } catch (err) {
      setError(err?.message || 'Unable to delete account.');
      setDeleting(false);
    }
  }

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
            {loading && <div className="panel-empty">Loading profile...</div>}
            {error && <div className="panel-error" role="alert">{error}</div>}
            {success && <div className="panel-success" role="status">{success}</div>}
            {!loading && username && (
              <>
                <form className="space-y-4" onSubmit={handleSave}>
                  <label className="field space-y-2">
                    <span className="field-label">Name</span>
                    <Input
                      value={name}
                      onChange={(event) => setName(event.target.value)}
                      autoComplete="name"
                    />
                  </label>
                  <label className="field space-y-2">
                    <span className="field-label">Primary email</span>
                    <Input
                      type="email"
                      value={primaryEmail}
                      onChange={(event) => setPrimaryEmail(event.target.value)}
                      autoComplete="email"
                    />
                  </label>
                  <div className="auth-actions">
                    <Button type="submit" disabled={saving}>
                      {saving ? 'Saving...' : 'Save profile'}
                    </Button>
                    <Button type="button" variant="ghost" onClick={onLogout}>
                      Logout
                    </Button>
                  </div>
                </form>

                <div className="space-y-3">
                  <div className="space-y-1">
                    <h3>Delete account</h3>
                    <p className="status">This removes the local user and revokes active sessions.</p>
                  </div>
                  <label className="field space-y-2">
                    <span className="field-label">Type your username to confirm</span>
                    <Input
                      value={deleteConfirmation}
                      onChange={(event) => setDeleteConfirmation(event.target.value)}
                      autoComplete="off"
                    />
                  </label>
                  <Button
                    type="button"
                    variant="destructive"
                    disabled={deleting || deleteConfirmation !== username}
                    onClick={handleDelete}
                  >
                    {deleting ? 'Deleting...' : 'Delete account'}
                  </Button>
                </div>
              </>
            )}
          </CardContent>
        </Card>
      )}
    </section>
  );
}
