import { useCallback, useEffect, useMemo, useState } from 'react';

import {
  createOrganization,
  deleteCurrentUser,
  fetchCurrentUser,
  fetchOrganizations,
  updateCurrentUser,
} from '../utils/api.js';
import { Badge } from './ui/badge.jsx';
import { Button } from './ui/button.jsx';
import { Card, CardContent, CardHeader, CardTitle } from './ui/card.jsx';
import { Input } from './ui/input.jsx';

function formatTimestamp(value) {
  if (!value) {
    return 'unknown';
  }
  if (typeof value === 'number') {
    const millis = value < 1_000_000_000_000 ? value * 1000 : value;
    return new Date(millis).toLocaleString();
  }
  return new Date(value).toLocaleString();
}

export default function ProfilePage({ username, onLogout, onRequireLogin }) {
  const [me, setMe] = useState(null);
  const [organizations, setOrganizations] = useState([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState('');
  const [formName, setFormName] = useState('');
  const [formEmail, setFormEmail] = useState('');
  const [profileSaving, setProfileSaving] = useState(false);
  const [profileError, setProfileError] = useState('');
  const [profileSuccess, setProfileSuccess] = useState('');
  const [orgName, setOrgName] = useState('');
  const [orgCreating, setOrgCreating] = useState(false);
  const [orgError, setOrgError] = useState('');
  const [deleteConfirmation, setDeleteConfirmation] = useState('');
  const [deleteError, setDeleteError] = useState('');
  const [deletePending, setDeletePending] = useState(false);

  const loadProfile = useCallback(async () => {
    setLoading(true);
    setError('');
    try {
      const [nextUser, nextOrganizations] = await Promise.all([
        fetchCurrentUser(),
        fetchOrganizations(),
      ]);
      setMe(nextUser);
      setOrganizations(nextOrganizations);
      setFormName(String(nextUser?.name || '').trim());
      setFormEmail(String(nextUser?.primary_email || nextUser?.primaryEmail || '').trim());
    } catch (err) {
      if (String(err?.message || '').includes('401')) {
        onRequireLogin?.();
        return;
      }
      setError(err?.message || 'Unable to load profile.');
    } finally {
      setLoading(false);
    }
  }, [onRequireLogin]);

  useEffect(() => {
    loadProfile();
  }, [loadProfile]);

  const usernameToConfirm = useMemo(
    () => String(me?.username || username || '').trim(),
    [me?.username, username],
  );

  async function handleProfileSave(event) {
    event.preventDefault();
    setProfileError('');
    setProfileSuccess('');
    setProfileSaving(true);
    try {
      const updated = await updateCurrentUser({
        name: formName.trim(),
        primaryEmail: formEmail.trim(),
      });
      setMe(updated);
      setFormName(String(updated?.name || '').trim());
      setFormEmail(String(updated?.primary_email || updated?.primaryEmail || '').trim());
      setProfileSuccess('Profile updated.');
    } catch (err) {
      setProfileError(err?.message || 'Unable to update profile.');
    } finally {
      setProfileSaving(false);
    }
  }

  async function handleCreateOrganization(event) {
    event.preventDefault();
    setOrgError('');
    setOrgCreating(true);
    try {
      await createOrganization({ name: orgName.trim() });
      setOrgName('');
      const nextOrganizations = await fetchOrganizations();
      setOrganizations(nextOrganizations);
    } catch (err) {
      setOrgError(err?.message || 'Unable to create organization.');
    } finally {
      setOrgCreating(false);
    }
  }

  async function handleDeleteAccount(event) {
    event.preventDefault();
    setDeleteError('');
    if (!usernameToConfirm || deleteConfirmation.trim() !== usernameToConfirm) {
      setDeleteError(`Type ${usernameToConfirm} to confirm account deletion.`);
      return;
    }
    setDeletePending(true);
    try {
      await deleteCurrentUser();
      await onLogout?.();
    } catch (err) {
      setDeleteError(err?.message || 'Unable to delete account.');
      setDeletePending(false);
    }
  }

  return (
    <section className="section auth-page" data-testid="profile-page">
      <div className="section-header">
        <Badge variant="secondary" className="eyebrow">Accounts</Badge>
        <h2>Profile</h2>
        <p>{username ? `Logged in as ${username}.` : 'Not logged in.'}</p>
      </div>

      <div className="profile-grid">
        <Card className="auth-card border-border/70">
          <CardHeader>
            <CardTitle className="text-xl">Local profile</CardTitle>
          </CardHeader>
          <CardContent className="space-y-4">
            {loading && <div className="status">Loading…</div>}
            {error && <div className="panel-error">{error}</div>}
            {!loading && !error && (
              <>
                <div className="kv">
                  <div className="kv-row">
                    <span className="kv-key">Username</span>
                    <span className="kv-val">{me?.username || username}</span>
                  </div>
                  <div className="kv-row">
                    <span className="kv-key">Created</span>
                    <span className="kv-val">
                      {formatTimestamp(me?.created_at || me?.createdAt)}
                    </span>
                  </div>
                </div>

                <form className="space-y-4" onSubmit={handleProfileSave}>
                  <label className="field">
                    <span className="field-label">Name</span>
                    <Input
                      type="text"
                      value={formName}
                      onChange={(event) => {
                        setFormName(event.target.value);
                        setProfileError('');
                        setProfileSuccess('');
                      }}
                      placeholder="Display name"
                    />
                  </label>
                  <label className="field">
                    <span className="field-label">Primary email</span>
                    <Input
                      type="email"
                      value={formEmail}
                      onChange={(event) => {
                        setFormEmail(event.target.value);
                        setProfileError('');
                        setProfileSuccess('');
                      }}
                      placeholder="name@example.com"
                    />
                  </label>
                  {profileError && <div className="panel-error">{profileError}</div>}
                  {profileSuccess && <div className="panel-empty">{profileSuccess}</div>}
                  <div className="auth-actions">
                    <Button type="submit" disabled={profileSaving}>
                      {profileSaving ? 'Saving…' : 'Save profile'}
                    </Button>
                    <Button type="button" variant="ghost" onClick={loadProfile}>
                      Refresh
                    </Button>
                    <Button type="button" variant="ghost" onClick={onLogout}>
                      Logout
                    </Button>
                  </div>
                </form>
              </>
            )}
          </CardContent>
        </Card>

        <Card className="auth-card border-border/70">
          <CardHeader>
            <CardTitle className="text-xl">Organizations</CardTitle>
          </CardHeader>
          <CardContent className="space-y-4">
            {!loading && !error && organizations.length === 0 && (
              <div className="panel-empty">No organizations yet.</div>
            )}
            {!loading && !error && organizations.length > 0 && (
              <ul className="org-list">
                {organizations.map((organization) => (
                  <li key={organization.slug || organization.id} className="org-item">
                    <div className="org-item-title">
                      <span className="org-name">{organization.name}</span>
                      <span className="org-slug">{organization.slug || organization.id}</span>
                    </div>
                    <div className="org-item-meta">
                      Created by {organization.owner_user_id || organization.ownerUserId || 'unknown'}
                    </div>
                  </li>
                ))}
              </ul>
            )}

            <form className="org-create" onSubmit={handleCreateOrganization}>
              <label className="field">
                <span className="field-label">New organization</span>
                <Input
                  type="text"
                  value={orgName}
                  onChange={(event) => setOrgName(event.target.value)}
                  placeholder="e.g. Acme"
                />
              </label>
              {orgError && <div className="panel-error">{orgError}</div>}
              <div className="auth-actions">
                <Button type="submit" disabled={orgCreating || !orgName.trim()}>
                  {orgCreating ? 'Creating…' : 'Create organization'}
                </Button>
              </div>
            </form>
          </CardContent>
        </Card>
      </div>

      <Card className="auth-card border-border/70">
        <CardHeader>
          <CardTitle className="text-xl">Danger zone</CardTitle>
        </CardHeader>
        <CardContent className="space-y-4">
          <p className="status">
            Deleting your local Gitslice account removes the local user record and revokes local sessions.
            It does not delete the upstream WorkOS identity.
          </p>
          <form className="space-y-4" onSubmit={handleDeleteAccount}>
            <label className="field">
              <span className="field-label">Type your username to confirm</span>
              <Input
                type="text"
                value={deleteConfirmation}
                onChange={(event) => {
                  setDeleteConfirmation(event.target.value);
                  setDeleteError('');
                }}
                placeholder={usernameToConfirm || 'username'}
              />
            </label>
            {deleteError && <div className="panel-error">{deleteError}</div>}
            <div className="auth-actions">
              <Button type="submit" variant="destructive" disabled={deletePending || !usernameToConfirm}>
                {deletePending ? 'Deleting…' : 'Delete account'}
              </Button>
            </div>
          </form>
        </CardContent>
      </Card>
    </section>
  );
}
